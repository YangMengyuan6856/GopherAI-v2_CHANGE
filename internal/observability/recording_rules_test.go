package observability

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type prometheusConfigContract struct {
	Global struct {
		ScrapeInterval     string `yaml:"scrape_interval"`
		ScrapeTimeout      string `yaml:"scrape_timeout"`
		EvaluationInterval string `yaml:"evaluation_interval"`
	} `yaml:"global"`
	RuleFiles    []string `yaml:"rule_files"`
	ScrapeConfig []struct {
		JobName     string `yaml:"job_name"`
		HonorLabels bool   `yaml:"honor_labels"`
		Static      []struct {
			Targets []string          `yaml:"targets"`
			Labels  map[string]string `yaml:"labels"`
		} `yaml:"static_configs"`
	} `yaml:"scrape_configs"`
}

type recordingRuleContract struct {
	Groups []struct {
		Name     string `yaml:"name"`
		Interval string `yaml:"interval"`
		Rules    []struct {
			Record string `yaml:"record"`
			Expr   string `yaml:"expr"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func TestPrometheusScrapeContractUsesOnlyLoopbackApplicationTargets(t *testing.T) {
	var config prometheusConfigContract
	decodeStrictYAML(t, observabilityFile(t, "prometheus.yml"), &config)
	if config.Global.ScrapeInterval != "15s" || config.Global.ScrapeTimeout != "5s" || config.Global.EvaluationInterval != "15s" {
		t.Fatalf("unexpected global Prometheus intervals: %+v", config.Global)
	}
	if len(config.RuleFiles) != 1 || config.RuleFiles[0] != "recording-rules.yml" {
		t.Fatalf("unexpected rule files: %v", config.RuleFiles)
	}
	got := map[string]string{}
	for _, scrape := range config.ScrapeConfig {
		if scrape.HonorLabels || len(scrape.Static) != 1 || len(scrape.Static[0].Targets) != 1 {
			t.Fatalf("scrape job must use one authoritative static target: %+v", scrape)
		}
		got[scrape.JobName] = scrape.Static[0].Targets[0] + "/" + scrape.Static[0].Labels["component"]
	}
	want := map[string]string{
		"gopherai-backend":      "127.0.0.1:9090/backend",
		"gopherai-index-worker": "127.0.0.1:9091/index_worker",
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected scrape jobs: %v", got)
	}
	for job, target := range want {
		if got[job] != target {
			t.Fatalf("scrape target %s=%q, want %q", job, got[job], target)
		}
	}
}

func TestRecordingRulesExposeStableBoundedAggregates(t *testing.T) {
	var config recordingRuleContract
	decodeStrictYAML(t, observabilityFile(t, "recording-rules.yml"), &config)
	required := []string{
		"gopherai:scrape_target_up", "gopherai:request_rate5m", "gopherai:request_success_rate5m",
		"gopherai:request_duration_p95_seconds5m", "gopherai:agent_population10m", "gopherai:agent_success_rate10m",
		"gopherai:tool_population10m", "gopherai:tool_success_rate10m", "gopherai:tool_duration_p95_seconds10m",
		"gopherai:rag_population15m", "gopherai:rag_grounded_answer_rate15m", "gopherai:rag_empty_rate15m",
		"gopherai:control_action_rate15m", "gopherai:webhook_delivery_rate15m", "gopherai:online_eval_population30m",
		"gopherai:online_eval_score_avg30m", "gopherai:feedback_accept_rate30m",
	}
	seen := map[string]string{}
	for _, group := range config.Groups {
		if strings.TrimSpace(group.Name) == "" || strings.TrimSpace(group.Interval) == "" || len(group.Rules) == 0 {
			t.Fatalf("invalid recording rule group: %+v", group)
		}
		for _, rule := range group.Rules {
			if !strings.HasPrefix(rule.Record, "gopherai:") || strings.TrimSpace(rule.Expr) == "" {
				t.Fatalf("invalid recording rule: %+v", rule)
			}
			if _, duplicate := seen[rule.Record]; duplicate {
				t.Fatalf("duplicate recording rule %s", rule.Record)
			}
			lower := strings.ToLower(rule.Record + " " + rule.Expr)
			for _, blocked := range []string{"tenant_id", "user_id", "session_id", "request_id", "trace_id", "run_id", "step_id", "call_id", "document_id", "case_id", "prompt", "query", "path", "url", "email", "ip"} {
				if strings.Contains(lower, blocked) {
					t.Fatalf("recording rule %s contains blocked dimension %s", rule.Record, blocked)
				}
			}
			seen[rule.Record] = rule.Expr
		}
	}
	missing := []string{}
	for _, name := range required {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(seen) != len(required) || len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("recording rule contract mismatch: got=%d want=%d missing=%v", len(seen), len(required), missing)
	}
}

func observabilityFile(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve recording rule test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "deploy", "observability", name))
}

func decodeStrictYAML(t *testing.T, path string, target any) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
