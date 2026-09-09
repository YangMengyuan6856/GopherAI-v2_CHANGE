package evaluation

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/harness"
	memorydomain "GopherAI/internal/memory"
	"GopherAI/internal/profilememory"
	"GopherAI/model"
)

const MemoryEvaluatorVersion = "memory-evaluator-v1"

type MemoryEvaluationMetrics struct {
	CaseCount               int     `json:"case_count"`
	RelevantMemoryRecall    float64 `json:"relevant_memory_recall"`
	StaleWrongInjectionRate float64 `json:"stale_wrong_injection_rate"`
	DeletedMemoryRecall     int     `json:"deleted_memory_recall"`
	CrossPrincipalLeakage   int     `json:"cross_principal_leakage"`
	ContextBudgetPassRate   float64 `json:"context_budget_pass_rate"`
	DeterministicReplayRate float64 `json:"deterministic_replay_rate"`
	LatencyP50MS            float64 `json:"latency_p50_ms"`
	LatencyP95MS            float64 `json:"latency_p95_ms"`
}

type MemoryEvaluationCaseResult struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`
	ExpectedKeys  []string `json:"expected_keys,omitempty"`
	ActualKeys    []string `json:"actual_keys,omitempty"`
	ForbiddenHits []string `json:"forbidden_hits,omitempty"`
	WithinBudget  bool     `json:"within_budget"`
	Deterministic bool     `json:"deterministic"`
	LatencyMS     float64  `json:"latency_ms"`
}

type MemoryEvaluationReport struct {
	EvaluatorVersion     string                       `json:"evaluator_version"`
	DatasetVersion       string                       `json:"dataset_version"`
	GeneratedAt          time.Time                    `json:"generated_at"`
	HumanReviewed        bool                         `json:"human_reviewed"`
	BaselineEligible     bool                         `json:"baseline_eligible"`
	TechnicalGatesPassed bool                         `json:"technical_gates_passed"`
	GateFailures         []string                     `json:"gate_failures,omitempty"`
	MetricNotes          map[string]string            `json:"metric_notes"`
	Metrics              MemoryEvaluationMetrics      `json:"metrics"`
	Cases                []MemoryEvaluationCaseResult `json:"cases"`
}

func EvaluateMemory(selector *profilememory.Selector, assembler *memorydomain.Assembler, cases []MemoryCase, summary MemoryDatasetSummary, generatedAt time.Time) MemoryEvaluationReport {
	if selector == nil {
		selector = profilememory.NewSelector()
	}
	if assembler == nil {
		assembler = memorydomain.NewAssembler()
	}
	report := MemoryEvaluationReport{
		EvaluatorVersion: MemoryEvaluatorVersion, DatasetVersion: MemoryDatasetVersion, GeneratedAt: generatedAt.UTC(), HumanReviewed: summary.HumanReviewed,
		MetricNotes: map[string]string{
			"relevant_memory_recall":     "Expected relevant keys returned divided by all expected relevant keys.",
			"stale_wrong_injection_rate": "Forbidden stale, conflicting, low-confidence or unrelated values injected divided by all forbidden values.",
			"deleted_memory_recall":      "Deleted fixture values present in rebuilt context; target is exactly zero.",
			"cross_principal_leakage":    "Other-user or other-tenant values present in context; target is exactly zero.",
		},
		Cases: make([]MemoryEvaluationCaseResult, 0, len(cases)),
	}
	now := generatedAt.UTC()
	tenantHash, userHash := harness.PrincipalHash("eval-tenant"), harness.PrincipalHash("eval-user")
	latencies := make([]float64, 0, len(cases))
	var expectedTotal, expectedHit, forbiddenTotal, forbiddenHit, budgetPass, deterministicPass int
	for _, item := range cases {
		facts := fixtureMemories(item, tenantHash, userHash, now)
		facts = removeDeletedFacts(facts, item.DeletedIDs)
		started := time.Now()
		selected := selector.Select(tenantHash, userHash, item.Query, item.Limit, now, facts)
		profileFacts := make([]memorydomain.ProfileFact, 0, len(selected))
		for _, fact := range selected {
			profileFacts = append(profileFacts, memorydomain.ProfileFact{Key: fact.Key, Value: fact.Value, Confidence: fact.Confidence})
		}
		assembly := assembler.Assemble(memorydomain.AssembleInput{
			SafetyRules: []string{"当前明确输入优先于旧记忆。"}, CurrentQuestion: item.Query,
			ProfileFacts: profileFacts, BudgetTokens: item.BudgetTokens,
		})
		replay := assembler.Assemble(memorydomain.AssembleInput{
			SafetyRules: []string{"当前明确输入优先于旧记忆。"}, CurrentQuestion: item.Query,
			ProfileFacts: profileFacts, BudgetTokens: item.BudgetTokens,
		})
		latency := float64(time.Since(started)) / float64(time.Millisecond)
		latencies = append(latencies, latency)
		actualKeys := includedProfileKeys(assembly.Included)
		forbiddenHits := forbiddenContextHits(assembly.Included, item.Expected.ForbiddenValues)
		withinBudget := !assembly.OverBudget && hasRequiredQuestion(assembly.Included, item.Query)
		deterministic := assembliesEqual(assembly, replay)
		result := MemoryEvaluationCaseResult{
			ID: item.ID, Category: item.Category, ExpectedKeys: append([]string(nil), item.Expected.IncludedKeys...),
			ActualKeys: actualKeys, ForbiddenHits: forbiddenHits, WithinBudget: withinBudget, Deterministic: deterministic, LatencyMS: latency,
		}
		report.Cases = append(report.Cases, result)
		expectedTotal += len(item.Expected.IncludedKeys)
		for _, expected := range item.Expected.IncludedKeys {
			if containsValue(actualKeys, expected) {
				expectedHit++
			}
		}
		forbiddenTotal += len(item.Expected.ForbiddenValues)
		forbiddenHit += len(forbiddenHits)
		if item.Category == "deleted" {
			report.Metrics.DeletedMemoryRecall += len(forbiddenHits)
		}
		if item.Category == "cross_principal" {
			report.Metrics.CrossPrincipalLeakage += len(forbiddenHits)
		}
		if withinBudget {
			budgetPass++
		}
		if deterministic {
			deterministicPass++
		}
	}
	count := float64(len(cases))
	report.Metrics.CaseCount = len(cases)
	if expectedTotal > 0 {
		report.Metrics.RelevantMemoryRecall = float64(expectedHit) / float64(expectedTotal)
	}
	if forbiddenTotal > 0 {
		report.Metrics.StaleWrongInjectionRate = float64(forbiddenHit) / float64(forbiddenTotal)
	}
	report.Metrics.ContextBudgetPassRate = float64(budgetPass) / count
	report.Metrics.DeterministicReplayRate = float64(deterministicPass) / count
	report.Metrics.LatencyP50MS = percentile(latencies, 0.50)
	report.Metrics.LatencyP95MS = percentile(latencies, 0.95)
	if report.Metrics.RelevantMemoryRecall < 0.90 {
		report.GateFailures = append(report.GateFailures, "relevant_memory_recall_below_0.90")
	}
	if report.Metrics.StaleWrongInjectionRate > 0.05 {
		report.GateFailures = append(report.GateFailures, "stale_wrong_injection_rate_above_0.05")
	}
	if report.Metrics.DeletedMemoryRecall != 0 {
		report.GateFailures = append(report.GateFailures, "deleted_memory_recall_nonzero")
	}
	if report.Metrics.CrossPrincipalLeakage != 0 {
		report.GateFailures = append(report.GateFailures, "cross_principal_leakage_nonzero")
	}
	if report.Metrics.ContextBudgetPassRate != 1 {
		report.GateFailures = append(report.GateFailures, "context_budget_pass_rate_below_1.0")
	}
	if report.Metrics.DeterministicReplayRate != 1 {
		report.GateFailures = append(report.GateFailures, "deterministic_replay_rate_below_1.0")
	}
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	report.BaselineEligible = report.TechnicalGatesPassed && report.HumanReviewed
	return report
}

func MarshalMemoryReport(report MemoryEvaluationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func fixtureMemories(item MemoryCase, tenantHash, userHash string, now time.Time) []model.EnvironmentMemory {
	result := make([]model.EnvironmentMemory, 0, len(item.Facts))
	for _, fact := range item.Facts {
		factTenant, factUser := tenantHash, userHash
		switch fact.OwnerScope {
		case "other_user":
			factUser = harness.PrincipalHash("other-user")
		case "other_tenant":
			factTenant = harness.PrincipalHash("other-tenant")
		}
		var expiresAt *time.Time
		if fact.Expiry == "fresh" {
			value := now.Add(24 * time.Hour)
			expiresAt = &value
		} else if fact.Expiry == "expired" {
			value := now.Add(-time.Hour)
			expiresAt = &value
		}
		result = append(result, model.EnvironmentMemory{
			ID: fact.ID, TenantIDHash: factTenant, UserIDHash: factUser, Key: fact.Key, Value: fact.Value,
			Status: fact.Status, Confidence: fact.Confidence, ExpiresAt: expiresAt,
			LastObservedAt: now.Add(time.Duration(fact.ObservedOrder) * time.Minute),
		})
	}
	return result
}

func removeDeletedFacts(items []model.EnvironmentMemory, deletedIDs []string) []model.EnvironmentMemory {
	deleted := make(map[string]struct{}, len(deletedIDs))
	for _, id := range deletedIDs {
		deleted[id] = struct{}{}
	}
	result := make([]model.EnvironmentMemory, 0, len(items))
	for _, item := range items {
		if _, remove := deleted[item.ID]; !remove {
			result = append(result, item)
		}
	}
	return result
}

func includedProfileKeys(items []memorydomain.ContextItem) []string {
	result := make([]string, 0, 5)
	for _, item := range items {
		if item.Kind != memorydomain.ContextProfile {
			continue
		}
		value := strings.TrimPrefix(item.Content, "confirmed_environment.")
		if split := strings.IndexByte(value, '='); split > 0 {
			result = append(result, value[:split])
		}
	}
	sort.Strings(result)
	return result
}

func forbiddenContextHits(items []memorydomain.ContextItem, forbidden []string) []string {
	hits := make([]string, 0)
	for _, value := range forbidden {
		for _, item := range items {
			if strings.Contains(item.Content, value) {
				hits = append(hits, value)
				break
			}
		}
	}
	return hits
}

func hasRequiredQuestion(items []memorydomain.ContextItem, query string) bool {
	for _, item := range items {
		if item.Kind == memorydomain.ContextQuestion && item.Required && item.Content == query {
			return true
		}
	}
	return false
}

func assembliesEqual(left, right memorydomain.ContextAssembly) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return bytesEqual(leftBytes, rightBytes)
}

func bytesEqual(left, right []byte) bool { return string(left) == string(right) }
