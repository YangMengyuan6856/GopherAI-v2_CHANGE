package incident

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/model"
)

type fakeRunReader struct {
	response diagnostic.RunResponse
	err      error
}

func (reader fakeRunReader) Get(context.Context, string, string) (diagnostic.RunResponse, error) {
	return reader.response, reader.err
}

type memoryRepository struct {
	writes    []confirmationWrite
	incidents map[string]model.ResolvedIncident
}

func (repository *memoryRepository) Confirm(_ context.Context, write confirmationWrite) (model.ResolvedIncident, bool, error) {
	for _, prior := range repository.writes {
		if prior.Feedback.ClientRequestID == write.Feedback.ClientRequestID {
			if prior.ResolutionSHA256 != write.ResolutionSHA256 {
				return model.ResolvedIncident{}, false, ErrIdempotencyConflict
			}
			return prior.Incident, false, nil
		}
	}
	repository.writes = append(repository.writes, write)
	if repository.incidents == nil {
		repository.incidents = map[string]model.ResolvedIncident{}
	}
	repository.incidents[write.Incident.SourceRunID] = write.Incident
	return write.Incident, true, nil
}

func (repository *memoryRepository) GetByRun(_ context.Context, _ string, runID string) (*model.ResolvedIncident, error) {
	value, exists := repository.incidents[runID]
	if !exists {
		return nil, nil
	}
	return &value, nil
}

func (*memoryRepository) GetByID(context.Context, string, string) (*model.ResolvedIncident, error) {
	return nil, nil
}
func (*memoryRepository) MarkIndexed(context.Context, string, int, time.Time) error { return nil }
func (*memoryRepository) RecordIndexFailure(context.Context, string, int, string, bool) error {
	return nil
}

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

func eligibleResponse() diagnostic.RunResponse {
	return diagnostic.RunResponse{Detail: harness.RunDetail{Run: harness.Run{
		RunID: "run-1", TenantIDHash: strings.Repeat("a", 64), UserIDHash: strings.Repeat("b", 64), SessionID: "session-1",
		TraceID: "trace-1", State: harness.StateSucceeded, StateVersion: 5,
	}}, Result: &diagnostic.Result{
		Version: diagnostic.SchemaVersion, Symptom: "Redis 返回 NOAUTH", Components: []string{"redis"}, ErrorSignatures: []string{"redis_noauth"},
		ConclusionStatus: diagnostic.ConclusionHypothesis, Hypotheses: []diagnostic.Hypothesis{{
			ID: "redis-auth", Cause: "Redis 凭据没有加载", Confidence: .95, Rationale: "NOAUTH 来自服务端。",
			Evidence:          []diagnostic.EvidenceReference{{ID: "user-observation:redis_noauth", SourceType: diagnostic.EvidenceUserObservation, Summary: "命中 NOAUTH"}},
			VerificationSteps: []diagnostic.VerificationStep{{ID: "ping", ActionType: diagnostic.ActionQuery, Instruction: "使用同源配置 PING。", ExpectedObservation: "PONG", FailureMeaning: "认证仍有误", ReadOnly: true}},
		}},
	}}
}

func TestPreviewIsReadOnlyAndRequiresExplicitConfirmation(t *testing.T) {
	repository := new(memoryRepository)
	service, _ := NewService(fakeRunReader{response: eligibleResponse()}, repository, fixedClock{value: time.Now().UTC()})
	proposal, err := service.Preview(context.Background(), "alice", "run-1", "redis-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.RequiresHumanConfirm || proposal.ExpectedStateVersion != 5 || len(repository.writes) != 0 {
		t.Fatalf("preview mutated memory or lost HITL gate: %+v writes=%d", proposal, len(repository.writes))
	}
}

func TestConfirmPersistsFeedbackIncidentAndOutboxOnce(t *testing.T) {
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	repository := new(memoryRepository)
	service, _ := NewService(fakeRunReader{response: eligibleResponse()}, repository, fixedClock{value: now})
	command := ConfirmCommand{RunID: "run-1", UserID: "alice", HypothesisID: "redis-auth", ExpectedStateVersion: 5, ClientRequestID: "confirm-1", Resolution: "已把 password=very-secret 改为正确的容器注入变量并验证 PONG"}
	first, err := service.Confirm(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Confirm(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || len(repository.writes) != 1 {
		t.Fatalf("confirmation was not idempotent: first=%v second=%v writes=%d", first.Created, second.Created, len(repository.writes))
	}
	write := repository.writes[0]
	if write.Incident.Status != StatusConfirmed || write.Incident.IndexStatus != IndexStatusPending || write.Event.Topic != IndexTopic || write.Event.Status != OutboxStatusPending {
		t.Fatalf("transactional records incomplete: %+v %+v", write.Incident, write.Event)
	}
	if strings.Contains(write.Incident.Resolution, "very-secret") || !strings.Contains(write.Incident.Resolution, "[REDACTED]") {
		t.Fatalf("confirmed resolution was not sanitized: %q", write.Incident.Resolution)
	}
}

func TestConfirmRejectsStaleVersionAndIneligibleRun(t *testing.T) {
	response := eligibleResponse()
	service, _ := NewService(fakeRunReader{response: response}, new(memoryRepository), fixedClock{value: time.Now().UTC()})
	_, err := service.Confirm(context.Background(), ConfirmCommand{RunID: "run-1", UserID: "alice", HypothesisID: "redis-auth", ExpectedStateVersion: 4, ClientRequestID: "confirm-1", Resolution: "已验证并解决认证配置"})
	if !errors.Is(err, harness.ErrRunConflict) {
		t.Fatalf("expected stale version conflict, got %v", err)
	}
	response.Detail.Run.State = harness.StateWaitingUser
	service, _ = NewService(fakeRunReader{response: response}, new(memoryRepository), fixedClock{value: time.Now().UTC()})
	_, err = service.Preview(context.Background(), "alice", "run-1", "redis-auth")
	if !errors.Is(err, ErrRunNotEligible) {
		t.Fatalf("expected ineligible run error, got %v", err)
	}
}
