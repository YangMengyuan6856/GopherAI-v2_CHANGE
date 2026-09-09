package diagnostic

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type caseStrategyRetriever struct {
	items []SimilarIncident
	err   error
	wait  bool
}

func (retriever caseStrategyRetriever) RetrieveSimilar(ctx context.Context, _, _ string, _ ExtractedInput, _ int) ([]SimilarIncident, error) {
	if retriever.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return append([]SimilarIncident{}, retriever.items...), retriever.err
}

func confirmedCase(score float64) SimilarIncident {
	return SimilarIncident{
		IncidentID: "incident-1", Symptom: "Redis returned NOAUTH", RootCause: "应用使用了错误的 Redis ACL 凭据",
		Resolution: "更新 Secret 后滚动重启并验证 PING", MatchedErrorSignatures: []string{"redis_noauth"},
		MatchedComponents: []string{"redis"}, Score: score, ConfirmedAt: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC),
	}
}

func TestCaseBasedStrategyPrioritizesOnlyStrongConfirmedMatch(t *testing.T) {
	strategy, err := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{items: []SimilarIncident{confirmedCase(.93)}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, baseline, err := NewAgent().Analyze("Redis NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	result, err := strategy.Analyze(context.Background(), "alice", "alice", "Redis NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseStrength != CaseStrengthStrong || result.PriorityRecommendation == nil || !result.PriorityRecommendation.AdvisoryOnly || result.PriorityRecommendation.HypothesisID == "" || result.AffectsLiveTraffic {
		t.Fatalf("strong case was not bounded advisory guidance: %+v", result)
	}
	if !reflect.DeepEqual(result.Baseline, baseline) {
		t.Fatal("case strategy mutated the standard diagnostic baseline")
	}
}

func TestCaseBasedStrategyKeepsWeakMatchAdvisory(t *testing.T) {
	strategy, _ := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{items: []SimilarIncident{confirmedCase(.70)}}, time.Second)
	result, err := strategy.Analyze(context.Background(), "alice", "alice", "Redis NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseStrength != CaseStrengthWeak || result.PriorityRecommendation != nil || result.ReasonCode != "case_match_advisory_only" {
		t.Fatalf("weak case improperly influenced hypotheses: %+v", result)
	}
}

func TestCaseBasedStrategySortsBeforeApplyingThreeCaseLimit(t *testing.T) {
	items := []SimilarIncident{confirmedCase(.50), confirmedCase(.60), confirmedCase(.70), confirmedCase(.96)}
	for index := range items {
		items[index].IncidentID = string(rune('a' + index))
	}
	strategy, _ := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{items: items}, time.Second)
	result, err := strategy.Analyze(context.Background(), "alice", "alice", "Redis NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cases) != 3 || result.Cases[0].Score != .96 || result.PriorityRecommendation == nil {
		t.Fatalf("strongest case was lost before bounded truncation: %+v", result)
	}
}

func TestCaseBasedStrategyFallsBackExplicitlyWhenRecallFails(t *testing.T) {
	strategy, _ := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{err: errors.New("redis unavailable")}, time.Second)
	result, err := strategy.Analyze(context.Background(), "alice", "alice", "Redis NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseMemoryStatus != CaseMemoryUnavailable || result.FallbackStrategy != StrategyName || result.ReasonCode != "case_recall_unavailable" || len(result.Baseline.Hypotheses) == 0 {
		t.Fatalf("case recall did not fail open to standard diagnosis: %+v", result)
	}
}

func TestCaseBasedStrategyBoundsRecallTimeout(t *testing.T) {
	strategy, _ := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{wait: true}, 10*time.Millisecond)
	started := time.Now()
	result, err := strategy.Analyze(context.Background(), "alice", "alice", "Redis NOAUTH Authentication required")
	if err != nil || time.Since(started) > 250*time.Millisecond || result.FallbackStrategy != StrategyName {
		t.Fatalf("case recall timeout was not bounded: result=%+v err=%v", result, err)
	}
}

func TestCaseBasedStrategyPropagatesCallerCancellation(t *testing.T) {
	strategy, _ := NewCaseBasedStrategy(NewAgent(), caseStrategyRetriever{wait: true}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := strategy.Analyze(ctx, "alice", "alice", "Redis NOAUTH"); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation became fallback success: %v", err)
	}
}
