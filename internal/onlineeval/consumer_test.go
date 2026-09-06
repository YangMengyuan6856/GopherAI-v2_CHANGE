package onlineeval

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryRepository struct {
	mu          sync.Mutex
	samples     map[string]model.OnlineEvaluationSample
	getError    error
	createBlock chan struct{}
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{samples: make(map[string]model.OnlineEvaluationSample)}
}

func (repository *memoryRepository) Create(_ context.Context, sample *model.OnlineEvaluationSample, _ *model.OutboxEvent) error {
	if repository.createBlock != nil {
		<-repository.createBlock
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.samples[sample.ID] = *sample
	return nil
}

func (repository *memoryRepository) Get(_ context.Context, id string) (model.OnlineEvaluationSample, error) {
	if repository.getError != nil {
		return model.OnlineEvaluationSample{}, repository.getError
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sample, found := repository.samples[id]
	if !found {
		return model.OnlineEvaluationSample{}, errors.New("not found")
	}
	return sample, nil
}

func (repository *memoryRepository) MarkEvaluating(_ context.Context, id string, attempt int, now time.Time) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sample := repository.samples[id]
	sample.Status, sample.Attempt, sample.UpdatedAt = StatusEvaluating, attempt, now
	repository.samples[id] = sample
	return true, nil
}

func (repository *memoryRepository) Complete(_ context.Context, id string, result EvaluationResult, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sample := repository.samples[id]
	sample.Status, sample.Overall, sample.JudgeModelVersion, sample.EvaluatedAt = StatusCompleted, result.Overall, result.ModelVersion, &now
	repository.samples[id] = sample
	return nil
}

func (repository *memoryRepository) Fail(_ context.Context, id, code string, attempt int, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sample := repository.samples[id]
	sample.Status, sample.LastErrorCode, sample.Attempt, sample.EvaluatedAt = StatusJudgeFailed, code, attempt, &now
	repository.samples[id] = sample
	return nil
}

func (repository *memoryRepository) MarkDead(_ context.Context, id, code string, attempt int, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sample := repository.samples[id]
	sample.Status, sample.LastErrorCode, sample.Attempt, sample.EvaluatedAt = StatusDead, code, attempt, &now
	repository.samples[id] = sample
	return nil
}

func (repository *memoryRepository) Summary(context.Context, time.Time) (Summary, error) {
	return Summary{}, nil
}
func (repository *memoryRepository) PruneExpired(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type fakeJudge struct {
	calls  int
	result evaluation.JudgeResult
	err    error
}

func (judge *fakeJudge) Judge(context.Context, evaluation.JudgeInput) (evaluation.JudgeResult, error) {
	judge.calls++
	return judge.result, judge.err
}

type fakeMetrics struct{ scores, failures int }

func (metrics *fakeMetrics) RecordOnlineEvaluation(string, evaluation.JudgeScores) { metrics.scores++ }
func (metrics *fakeMetrics) RecordOnlineEvaluationFailure(string, string)          { metrics.failures++ }

func TestSimulationTraversesConsumerWithoutModelOrProductionMetric(t *testing.T) {
	repository := newMemoryRepository()
	judge := new(fakeJudge)
	metrics := new(fakeMetrics)
	processor, err := NewProcessor(repository, judge, metrics, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := NewConsumer(processor, DefaultMaximumAttempts)
	if err != nil {
		t.Fatal(err)
	}
	output := testOutput("rag_fast")
	sample, event, err := BuildSample(output, nil, decide(output, nil, "user_downvote"), time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	repository.samples[sample.ID] = sample
	result := consumer.Handle(context.Background(), envelopeBody(event))
	completed := repository.samples[sample.ID]
	if result.Action != jobqueue.ActionAck || completed.Status != StatusCompleted || completed.JudgeModelVersion != "no-model-call" {
		t.Fatalf("simulation did not complete async consumer path: action=%s sample=%+v", result.Action, completed)
	}
	if judge.calls != 0 || metrics.scores != 0 || metrics.failures != 0 {
		t.Fatalf("simulation polluted model or production metrics: judge=%d metrics=%+v", judge.calls, metrics)
	}
}

func TestJudgeFailureIsPersistedAndAcknowledgedWithoutNeutralScore(t *testing.T) {
	repository := newMemoryRepository()
	judge := &fakeJudge{result: evaluation.JudgeResult{ErrorCode: "judge_timeout"}, err: errors.New("timeout")}
	metrics := new(fakeMetrics)
	processor, _ := NewProcessor(repository, judge, metrics, time.Now)
	consumer, _ := NewConsumer(processor, DefaultMaximumAttempts)
	output := testOutput("rag_fast")
	sample, event, err := BuildSample(output, nil, decide(output, nil, "user_downvote"), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	repository.samples[sample.ID] = sample
	result := consumer.Handle(context.Background(), envelopeBody(event))
	failed := repository.samples[sample.ID]
	if result.Action != jobqueue.ActionAck || failed.Status != StatusJudgeFailed || failed.Overall != 0 || metrics.failures != 1 {
		t.Fatalf("judge failure was not explicitly persisted: action=%s sample=%+v metrics=%+v", result.Action, failed, metrics)
	}
}

func TestRepositoryFailureRetriesWithBoundedAttempt(t *testing.T) {
	repository := newMemoryRepository()
	repository.getError = errors.New("mysql unavailable")
	processor, _ := NewProcessor(repository, new(fakeJudge), new(fakeMetrics), time.Now)
	consumer, _ := NewConsumer(processor, DefaultMaximumAttempts)
	event := model.OutboxEvent{ID: "event-1", EventType: EventType, AggregateID: "sample-1", AggregateVersion: 1, PayloadJSON: `{"schema_version":"online-evaluation-sample-v1","sample_id":"sample-1","payload_hash":"hash"}`}
	result := consumer.Handle(context.Background(), envelopeBody(event))
	if result.Action != jobqueue.ActionRetry || result.Code != ErrorRepository {
		t.Fatalf("repository failure should retry, got %+v", result)
	}
}

func TestObserverRecordDoesNotBlockOnPersistence(t *testing.T) {
	repository := newMemoryRepository()
	repository.createBlock = make(chan struct{})
	observer := NewObserver(repository, 1, time.Now)
	output := testOutput("rag_fast")
	output.Result.Confidence = .2 // always selected
	started := time.Now()
	observer.Record(output, nil)
	observer.Record(output, nil)
	observer.Record(output, nil)
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("observer blocked the formal response path for %s", elapsed)
	}
	close(repository.createBlock)
}

func TestObserverExcludesBoundedPerformanceProbeFromOnlineQualitySamples(t *testing.T) {
	repository := newMemoryRepository()
	observer := NewObserver(repository, 1, time.Now)
	output := testOutput("legacy_chat")
	output.Request.UserID = performanceProbeUser
	output.Result.Confidence = .1
	observer.Record(output, nil)
	time.Sleep(10 * time.Millisecond)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.samples) != 0 {
		t.Fatalf("performance probe polluted online evaluation samples: %d", len(repository.samples))
	}
}

func envelopeBody(event model.OutboxEvent) []byte {
	payload := json.RawMessage(event.PayloadJSON)
	body, _ := json.Marshal(jobqueue.Envelope{
		SchemaVersion: contract.SchemaVersion, EventID: event.ID, EventType: event.EventType, TraceID: event.TraceID,
		TenantID: event.TenantID, AggregateID: event.AggregateID, AggregateVersion: event.AggregateVersion, Payload: payload,
	})
	return body
}
