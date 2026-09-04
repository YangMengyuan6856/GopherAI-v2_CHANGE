package diagnostic

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type stubCaseRetriever struct {
	items []SimilarIncident
	err   error
}

func (retriever stubCaseRetriever) RetrieveSimilar(context.Context, string, string, ExtractedInput, int) ([]SimilarIncident, error) {
	return retriever.items, retriever.err
}

func TestCaseRecallCannotMutateCurrentDiagnosticConclusion(t *testing.T) {
	workflow := &Workflow{caseRetriever: stubCaseRetriever{items: []SimilarIncident{{
		IncidentID: "incident-1", Symptom: "Redis 返回 NOAUTH", RootCause: "历史根因", Resolution: "历史解决办法",
		MatchedErrorSignatures: []string{"redis_noauth"}, MatchedComponents: []string{"redis"}, Score: 1,
		ConfirmedAt: time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC),
	}}}}
	_, result, err := NewAgent().Analyze("Docker 中 Redis 返回 NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	original := result
	workflow.enrichWithCases(context.Background(), "alice", "alice", ExtractedInput{ErrorSignatures: []string{"redis_noauth"}, Components: []string{"redis"}}, &result)
	if result.CaseMemoryStatus != CaseMemoryHit || len(result.SimilarIncidents) != 1 {
		t.Fatalf("expected one advisory recall: %#v", result)
	}
	result.CaseMemoryStatus, result.CaseMemoryPolicy, result.SimilarIncidents = "", "", nil
	if !reflect.DeepEqual(result, original) {
		t.Fatalf("case recall mutated the current diagnostic result\nwant: %#v\ngot:  %#v", original, result)
	}
}

func TestCaseRecallFailureIsExplicitAndFailOpen(t *testing.T) {
	workflow := &Workflow{caseRetriever: stubCaseRetriever{err: errors.New("database unavailable")}}
	_, result, err := NewAgent().Analyze("Redis 返回 NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	workflow.enrichWithCases(context.Background(), "alice", "alice", ExtractedInput{ErrorSignatures: []string{"redis_noauth"}}, &result)
	if result.CaseMemoryStatus != CaseMemoryUnavailable || len(result.SimilarIncidents) != 0 || len(result.Hypotheses) == 0 {
		t.Fatalf("recall failure should not fail or erase diagnosis: %#v", result)
	}
}

func TestInvalidCaseRetrieverPayloadFailsClosed(t *testing.T) {
	workflow := &Workflow{caseRetriever: stubCaseRetriever{items: []SimilarIncident{{IncidentID: "incident-1", Score: 2}}}}
	_, result, err := NewAgent().Analyze("Redis 返回 NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	workflow.enrichWithCases(context.Background(), "alice", "alice", ExtractedInput{}, &result)
	if result.CaseMemoryStatus != CaseMemoryUnavailable || len(result.SimilarIncidents) != 0 {
		t.Fatalf("invalid retrieved memory must not cross the response boundary: %#v", result)
	}
}
