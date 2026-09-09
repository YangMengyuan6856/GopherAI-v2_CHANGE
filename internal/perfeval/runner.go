package perfeval

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	chatTarget    = "http://127.0.0.1:9090/api/v1/chat/auto/stream"
	metricsTarget = "http://127.0.0.1:9090/metrics"
	pprofTarget   = "http://127.0.0.1:6060/debug/pprof"
)

type Config struct {
	HotRequests    int
	HotConcurrency int
	RequestTimeout time.Duration
	ProfileSeconds int
	OutputRoot     string
	Token          string
	Now            func() time.Time
	Cleanup        func(context.Context) error
}

type Runner struct{ client *http.Client }

func NewRunner() *Runner { return &Runner{client: &http.Client{Timeout: 125 * time.Second}} }

func ValidateConfig(config Config) error {
	if config.HotRequests < 1 || config.HotRequests > 48 || config.HotConcurrency < 1 || config.HotConcurrency > 5 || config.HotConcurrency > config.HotRequests {
		return errors.New("hot requests must be 1..48 and concurrency must be 1..5 and no greater than requests")
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > 120*time.Second || config.ProfileSeconds < 1 || config.ProfileSeconds > 15 {
		return errors.New("request timeout or profile duration is outside bounded limits")
	}
	if config.OutputRoot != "/root/GopherAI_Runtime/perf" || strings.TrimSpace(config.Token) == "" || config.Cleanup == nil {
		return errors.New("fixed output root, a short-lived signed token and synthetic-data cleanup are required")
	}
	return nil
}

func (runner *Runner) Run(ctx context.Context, config Config) (Report, error) {
	if err := ValidateConfig(config); err != nil {
		return Report{}, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	manifest, err := readReleaseManifest("release-manifest.json")
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(filepath.Join(config.OutputRoot, manifest.ID), 0700); err != nil {
		return Report{}, err
	}
	if sameReleaseReport(filepath.Join(config.OutputRoot, "latest.json"), manifest.ID) || fileExists(filepath.Join(config.OutputRoot, manifest.ID, "report.json")) {
		return Report{}, errors.New("a performance report already exists for this release")
	}
	lockPath := filepath.Join(config.OutputRoot, manifest.ID, "run.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Report{}, fmt.Errorf("acquire release performance lock: %w", err)
	}
	_ = lock.Close()
	defer os.Remove(lockPath)
	if err := config.Cleanup(ctx); err != nil {
		return Report{}, fmt.Errorf("clean stale synthetic probe data: %w", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = config.Cleanup(cleanupContext)
	}()
	runtimeBefore, err := inspectRuntime(ctx, runner.client)
	if err != nil {
		return Report{}, err
	}
	coldEligible := runtimeBefore.ProcessUptimeSeconds <= 600

	profileDir := filepath.Join(config.OutputRoot, manifest.ID)
	cpuResult := make(chan profileResult, 1)
	go func() {
		cpuResult <- runner.capture(ctx, fmt.Sprintf("%s/profile?seconds=%d", pprofTarget, config.ProfileSeconds), filepath.Join(profileDir, "cpu.pprof"), "cpu")
	}()

	coldBefore, err := scrapeCounters(ctx, runner.client)
	if err != nil {
		return Report{}, err
	}
	coldRun := runner.runPhase(ctx, config, 1, 1)
	coldAfter, err := scrapeCounters(ctx, runner.client)
	if err != nil {
		return Report{}, err
	}

	_ = runner.runPhase(ctx, config, 1, 1) // one excluded warm-up for the hot path
	hotBefore, err := scrapeCounters(ctx, runner.client)
	if err != nil {
		return Report{}, err
	}
	hotRun := runner.runPhase(ctx, config, config.HotRequests, config.HotConcurrency)
	hotAfter, err := scrapeCounters(ctx, runner.client)
	if err != nil {
		return Report{}, err
	}

	profiles := []ProfileArtifact{}
	cpu := <-cpuResult
	if cpu.err != nil {
		return Report{}, fmt.Errorf("capture cpu profile: %w", cpu.err)
	}
	profiles = append(profiles, cpu.artifact)
	for _, item := range []struct{ url, path, kind string }{
		{pprofTarget + "/heap", filepath.Join(profileDir, "heap.pprof"), "heap"},
		{pprofTarget + "/goroutine?debug=1", filepath.Join(profileDir, "goroutine.txt"), "goroutine"},
	} {
		captured := runner.capture(ctx, item.url, item.path, item.kind)
		if captured.err != nil {
			return Report{}, fmt.Errorf("capture %s profile: %w", item.kind, captured.err)
		}
		profiles = append(profiles, captured.artifact)
	}
	runtimeAfter, _ := inspectRuntime(ctx, runner.client)
	runtimeBefore.MemoryAvailableAfterBytes = runtimeAfter.MemoryAvailableBeforeBytes
	cleanupErr := config.Cleanup(ctx)

	coldTotals, coldTTFT := successfulTimings(coldRun.samples)
	hotTotals, hotTTFT := successfulTimings(hotRun.samples)
	cold := NewPhase("release_first_request", "部署后当前 release 的首个受控请求；不声称清空 Redis、OS page cache 或模型供应商缓存", 1, 1, 0, coldTotals, coldTTFT, coldAfter.ModelCalls-coldBefore.ModelCalls, coldAfter.Tokens-coldBefore.Tokens)
	hot := NewPhase("warmed_route", "先执行 1 个不计入指标的 warm-up，再按固定并发测量", config.HotRequests, config.HotConcurrency, 1, hotTotals, hotTTFT, hotAfter.ModelCalls-hotBefore.ModelCalls, hotAfter.Tokens-hotBefore.Tokens)
	cold.DurationMS, hot.DurationMS = coldRun.durationMS, hotRun.durationMS
	cold.ObservedRoutes, hot.ObservedRoutes = observedRoutes(coldRun.samples), observedRoutes(hotRun.samples)
	cold.ObservedModels, hot.ObservedModels = observedModels(coldBefore, coldAfter), observedModels(hotBefore, hotAfter)
	allPassed := cold.Errors == 0 && hot.Errors == 0
	profilesCaptured := len(profiles) == 3
	failures := []string{}
	if !coldEligible {
		failures = append(failures, "release_first_request_not_eligible")
	}
	if !allPassed {
		failures = append(failures, "request_failure_observed")
	}
	if !profilesCaptured {
		failures = append(failures, "profile_artifact_missing")
	}
	if cleanupErr != nil {
		failures = append(failures, "synthetic_data_cleanup_failed")
	}
	report := Report{
		SchemaVersion: SchemaVersion, RunnerVersion: RunnerVersion, GeneratedAt: config.Now().UTC(), Mode: "bounded_loopback_acceptance",
		Release: manifest, Runtime: runtimeBefore, Cold: cold, Hot: hot, Profiles: profiles,
		Gates:       Gates{ColdEligible: coldEligible, AllRequestsPassed: allPassed, ProfilesCaptured: profilesCaptured, SyntheticDataCleaned: cleanupErr == nil, TechnicalPassed: coldEligible && allPassed && profilesCaptured && cleanupErr == nil, Failures: failures},
		Guardrails:  []string{"loopback_target_only", "max_50_total_requests", "max_5_concurrency", "no_stored_credentials", "no_prompt_or_answer_persisted", "synthetic_rows_cleaned", "pprof_loopback_only"},
		Limitations: []string{"冷路径仅表示当前发布后的首个受控请求，不等于主机冷启动或 Redis/页缓存已清空。", "模型服务是外部依赖，本报告描述该时刻的端到端样本，不推断长期容量上限。", "Token 指标沿用应用内估算口径，不冒充供应商账单。", "模型调用与 Token 为进程级计数器差值；同一时段若有真实用户请求，可能包含其增量。"},
	}
	if err := Finalize(&report); err != nil {
		return Report{}, err
	}
	return report, nil
}

type sample struct {
	totalMS, ttftMS float64
	route           Route
	err             error
}

type phaseRun struct {
	samples    []sample
	durationMS float64
}

func (runner *Runner) runPhase(parent context.Context, config Config, requests, concurrency int) phaseRun {
	started := time.Now()
	jobs := make(chan int)
	results := make(chan sample, requests)
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				ctx, cancel := context.WithTimeout(parent, config.RequestTimeout)
				results <- runner.request(ctx, config.Token)
				cancel()
			}
		}()
	}
	for item := 0; item < requests; item++ {
		jobs <- item
	}
	close(jobs)
	workers.Wait()
	close(results)
	output := make([]sample, 0, requests)
	for result := range results {
		output = append(output, result)
	}
	return phaseRun{samples: output, durationMS: float64(time.Since(started).Microseconds()) / 1000}
}

func (runner *Runner) request(ctx context.Context, token string) sample {
	payload, _ := json.Marshal(map[string]any{"message": "只回复 PERFORMANCE-PROBE-OK", "client_request_id": fmt.Sprintf("perf-%d", time.Now().UnixNano()), "debug": false, "knowledge_required": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatTarget, bytes.NewReader(payload))
	if err != nil {
		return sample{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	started := time.Now()
	response, err := runner.client.Do(req)
	if err != nil {
		return sample{err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return sample{err: fmt.Errorf("unexpected status %d", response.StatusCode)}
	}
	firstDelta := time.Time{}
	deltaSeen, finalSeen, route, err := ScanStreamEvents(response.Body, func() {
		if firstDelta.IsZero() {
			firstDelta = time.Now()
		}
	})
	if err != nil {
		return sample{err: err}
	}
	if !deltaSeen || !finalSeen {
		return sample{err: errors.New("stream missed delta or final event")}
	}
	return sample{totalMS: float64(time.Since(started).Microseconds()) / 1000, ttftMS: float64(firstDelta.Sub(started).Microseconds()) / 1000, route: route}
}

func ScanStreamEvents(reader io.Reader, onFirstDelta func()) (bool, bool, Route, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	deltaSeen, finalSeen := false, false
	currentEvent := ""
	route := Route{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "event: delta":
			currentEvent = "delta"
			if !deltaSeen && onFirstDelta != nil {
				onFirstDelta()
			}
			deltaSeen = true
		case "event: final":
			currentEvent = "final"
			finalSeen = true
		default:
			if currentEvent == "final" && strings.HasPrefix(line, "data: ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &route); err != nil {
					return false, false, Route{}, err
				}
			}
		}
	}
	if finalSeen && strings.TrimSpace(route.Strategy) == "" {
		return false, false, Route{}, errors.New("final stream event omitted route identity")
	}
	return deltaSeen, finalSeen, route, scanner.Err()
}

func successfulTimings(samples []sample) ([]float64, []float64) {
	totals, ttfts := []float64{}, []float64{}
	for _, item := range samples {
		if item.err == nil {
			totals = append(totals, item.totalMS)
			ttfts = append(ttfts, item.ttftMS)
		}
	}
	return totals, ttfts
}

func observedRoutes(samples []sample) []Route {
	seen := map[string]bool{}
	routes := []Route{}
	for _, item := range samples {
		if item.err != nil || item.route.Strategy == "" {
			continue
		}
		key := item.route.Strategy + "\x00" + item.route.StrategyVersion + "\x00" + item.route.PolicyVersion
		if !seen[key] {
			seen[key] = true
			routes = append(routes, item.route)
		}
	}
	sort.Slice(routes, func(left, right int) bool { return routes[left].Strategy < routes[right].Strategy })
	return routes
}

type counters struct {
	ModelCalls, Tokens, ProcessStart float64
	ModelCallsByAlias                map[string]float64
}

func scrapeCounters(ctx context.Context, client *http.Client) (counters, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, metricsTarget, nil)
	response, err := client.Do(req)
	if err != nil {
		return counters{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return counters{}, fmt.Errorf("metrics status %d", response.StatusCode)
	}
	return ParsePrometheus(response.Body)
}

func ParsePrometheus(reader io.Reader) (counters, error) {
	result := counters{ModelCallsByAlias: map[string]float64{}}
	scanner := bufio.NewScanner(io.LimitReader(reader, 8*1024*1024))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if index := strings.IndexByte(name, '{'); index >= 0 {
			name = name[:index]
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch name {
		case "gopherai_model_calls_total":
			result.ModelCalls += value
			if alias := prometheusLabel(fields[0], "model"); alias != "" {
				result.ModelCallsByAlias[alias] += value
			}
		case "gopherai_model_tokens_total":
			result.Tokens += value
		case "process_start_time_seconds":
			result.ProcessStart = value
		}
	}
	return result, scanner.Err()
}

var prometheusLabelPattern = regexp.MustCompile(`(?:^|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)="([^"]*)"`)

func prometheusLabel(metric, wanted string) string {
	start, end := strings.IndexByte(metric, '{'), strings.LastIndexByte(metric, '}')
	if start < 0 || end <= start {
		return ""
	}
	for _, match := range prometheusLabelPattern.FindAllStringSubmatch(metric[start+1:end], -1) {
		if len(match) == 3 && match[1] == wanted {
			return match[2]
		}
	}
	return ""
}

func observedModels(before, after counters) []string {
	models := []string{}
	for alias, current := range after.ModelCallsByAlias {
		if current-before.ModelCallsByAlias[alias] > 0 {
			models = append(models, alias)
		}
	}
	sort.Strings(models)
	return models
}

func inspectRuntime(ctx context.Context, client *http.Client) (Runtime, error) {
	counter, err := scrapeCounters(ctx, client)
	if err != nil {
		return Runtime{}, err
	}
	total, available, err := readMemInfo("/proc/meminfo")
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{Target: chatTarget, GoVersion: runtime.Version(), CPUCores: runtime.NumCPU(), MemoryTotalBytes: total, MemoryAvailableBeforeBytes: available, ProcessUptimeSeconds: mathMax(0, float64(time.Now().Unix())-counter.ProcessStart)}, nil
}

func readMemInfo(path string) (uint64, uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			value, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr == nil {
				values[strings.TrimSuffix(fields[0], ":")] = value * 1024
			}
		}
	}
	if values["MemTotal"] == 0 || values["MemAvailable"] == 0 {
		return 0, 0, errors.New("meminfo is incomplete")
	}
	return values["MemTotal"], values["MemAvailable"], nil
}

type profileResult struct {
	artifact ProfileArtifact
	err      error
}

func (runner *Runner) capture(ctx context.Context, url, path, kind string) profileResult {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	response, err := runner.client.Do(req)
	if err != nil {
		return profileResult{err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return profileResult{err: fmt.Errorf("pprof status %d", response.StatusCode)}
	}
	file, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return profileResult{err: err}
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, 64*1024*1024))
	closeErr := file.Close()
	if copyErr != nil {
		return profileResult{err: copyErr}
	}
	if closeErr != nil {
		return profileResult{err: closeErr}
	}
	if written < 1 {
		return profileResult{err: errors.New("empty pprof artifact")}
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return profileResult{err: err}
	}
	return profileResult{artifact: ProfileArtifact{Kind: kind, Path: filepath.ToSlash(path), Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil))}}
}

func readReleaseManifest(path string) (Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Release{}, err
	}
	var raw struct {
		ReleaseID     string `json:"release_id"`
		GitSHA        string `json:"git_sha"`
		BuildStrategy string `json:"build_strategy"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Release{}, err
	}
	if raw.ReleaseID == "" {
		return Release{}, errors.New("release manifest is incomplete")
	}
	return Release{ID: raw.ReleaseID, GitSHA: raw.GitSHA, BuildStrategy: raw.BuildStrategy}, nil
}

func sameReleaseReport(path, releaseID string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var report Report
	return json.Unmarshal(data, &report) == nil && report.Release.ID == releaseID
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func mathMax(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
