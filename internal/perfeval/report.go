package perfeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion = "performance-evaluation-report-v1"
	RunnerVersion = "bounded-loopback-perf-v1"
)

type Release struct {
	ID            string `json:"id"`
	GitSHA        string `json:"git_sha"`
	BuildStrategy string `json:"build_strategy"`
}

type Runtime struct {
	Target                     string  `json:"target"`
	GoVersion                  string  `json:"go_version"`
	CPUCores                   int     `json:"cpu_cores"`
	MemoryTotalBytes           uint64  `json:"memory_total_bytes"`
	MemoryAvailableBeforeBytes uint64  `json:"memory_available_before_bytes"`
	MemoryAvailableAfterBytes  uint64  `json:"memory_available_after_bytes"`
	ProcessUptimeSeconds       float64 `json:"process_uptime_seconds"`
}

type Latency struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

type Phase struct {
	Name             string   `json:"name"`
	Definition       string   `json:"definition"`
	Requests         int      `json:"requests"`
	Concurrency      int      `json:"concurrency"`
	WarmupsExcluded  int      `json:"warmups_excluded"`
	DurationMS       float64  `json:"duration_ms"`
	Successes        int      `json:"successes"`
	Errors           int      `json:"errors"`
	SuccessRate      float64  `json:"success_rate"`
	TotalLatency     Latency  `json:"total_latency"`
	TTFT             Latency  `json:"ttft"`
	ModelCalls       float64  `json:"model_calls"`
	EstimatedTokens  float64  `json:"estimated_tokens"`
	ModelCallsPer100 float64  `json:"model_calls_per_100_successes"`
	TokensPer100     float64  `json:"estimated_tokens_per_100_successes"`
	ObservedRoutes   []Route  `json:"observed_routes"`
	ObservedModels   []string `json:"observed_model_aliases"`
}

type Route struct {
	Strategy        string `json:"strategy"`
	StrategyVersion string `json:"strategy_version"`
	PolicyVersion   string `json:"policy_version"`
}

type ProfileArtifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Gates struct {
	ColdEligible         bool     `json:"cold_eligible"`
	AllRequestsPassed    bool     `json:"all_requests_passed"`
	ProfilesCaptured     bool     `json:"profiles_captured"`
	SyntheticDataCleaned bool     `json:"synthetic_data_cleaned"`
	TechnicalPassed      bool     `json:"technical_passed"`
	Failures             []string `json:"failures"`
}

type Report struct {
	SchemaVersion string            `json:"schema_version"`
	RunnerVersion string            `json:"runner_version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	ReportSHA256  string            `json:"report_sha256"`
	Mode          string            `json:"mode"`
	Release       Release           `json:"release"`
	Runtime       Runtime           `json:"runtime"`
	Cold          Phase             `json:"cold"`
	Hot           Phase             `json:"hot"`
	Profiles      []ProfileArtifact `json:"profiles"`
	Gates         Gates             `json:"gates"`
	Guardrails    []string          `json:"guardrails"`
	Limitations   []string          `json:"limitations"`
}

func NewPhase(name, definition string, requests, concurrency, warmups int, totals, ttfts []float64, modelCalls, tokens float64) Phase {
	successes := len(totals)
	errorsCount := requests - successes
	phase := Phase{
		Name: name, Definition: definition, Requests: requests, Concurrency: concurrency, WarmupsExcluded: warmups,
		Successes: successes, Errors: errorsCount, TotalLatency: Percentiles(totals), TTFT: Percentiles(ttfts),
		ModelCalls: modelCalls, EstimatedTokens: tokens,
	}
	if requests > 0 {
		phase.SuccessRate = float64(successes) / float64(requests)
	}
	if successes > 0 {
		phase.ModelCallsPer100 = modelCalls * 100 / float64(successes)
		phase.TokensPer100 = tokens * 100 / float64(successes)
	}
	return phase
}

func Percentiles(values []float64) Latency {
	if len(values) == 0 {
		return Latency{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return Latency{P50MS: percentile(sorted, .50), P95MS: percentile(sorted, .95), P99MS: percentile(sorted, .99)}
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 1 {
		return round(sorted[0])
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	value := sorted[lower]
	if upper != lower {
		value += (sorted[upper] - sorted[lower]) * (position - float64(lower))
	}
	return round(value)
}

func round(value float64) float64 { return math.Round(value*1000) / 1000 }

func Finalize(report *Report) error {
	if report == nil {
		return errors.New("performance report is required")
	}
	report.ReportSHA256 = ""
	if err := Validate(*report, false); err != nil {
		return err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	report.ReportSHA256 = hex.EncodeToString(digest[:])
	return Validate(*report, true)
}

func Validate(report Report, requireHash bool) error {
	if report.SchemaVersion != SchemaVersion || report.RunnerVersion != RunnerVersion || report.GeneratedAt.IsZero() || report.Mode != "bounded_loopback_acceptance" {
		return errors.New("performance report identity is invalid")
	}
	if strings.TrimSpace(report.Release.ID) == "" || strings.TrimSpace(report.Runtime.Target) != "http://127.0.0.1:9090/api/v1/chat/auto/stream" {
		return errors.New("performance report release or fixed target is invalid")
	}
	for _, phase := range []Phase{report.Cold, report.Hot} {
		if phase.Requests < 1 || phase.Requests > 48 || phase.Concurrency < 1 || phase.Concurrency > 5 || phase.Successes+phase.Errors != phase.Requests || phase.SuccessRate < 0 || phase.SuccessRate > 1 || phase.DurationMS <= 0 {
			return fmt.Errorf("performance phase %q is outside bounded limits", phase.Name)
		}
		if phase.Successes > 0 && len(phase.ObservedRoutes) == 0 {
			return fmt.Errorf("performance phase %q is missing its observed route", phase.Name)
		}
	}
	if report.Cold.Requests != 1 || report.Cold.Concurrency != 1 || report.Hot.WarmupsExcluded != 1 {
		return errors.New("cold/hot cache definitions are inconsistent")
	}
	if len(report.Profiles) != 3 || len(report.Guardrails) < 4 || len(report.Limitations) < 2 {
		return errors.New("performance evidence or limitations are incomplete")
	}
	profileKinds := map[string]bool{}
	for _, artifact := range report.Profiles {
		if artifact.Bytes < 1 || len(artifact.SHA256) != 64 || !strings.HasPrefix(artifact.Path, "/root/GopherAI_Runtime/perf/") {
			return errors.New("performance profile artifact is invalid")
		}
		if profileKinds[artifact.Kind] || (artifact.Kind != "cpu" && artifact.Kind != "heap" && artifact.Kind != "goroutine") {
			return errors.New("performance profile kinds are invalid")
		}
		profileKinds[artifact.Kind] = true
	}
	if report.Gates.TechnicalPassed != (report.Gates.ColdEligible && report.Gates.AllRequestsPassed && report.Gates.ProfilesCaptured && report.Gates.SyntheticDataCleaned) {
		return errors.New("performance gates are inconsistent")
	}
	if requireHash {
		supplied := report.ReportSHA256
		report.ReportSHA256 = ""
		encoded, err := json.Marshal(report)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		if supplied != hex.EncodeToString(digest[:]) {
			return errors.New("performance report hash is invalid")
		}
	}
	return nil
}
