package feedback

import (
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

type memoryRepository struct {
	run      model.AgentRun
	feedback *model.UserFeedbackEvent
	sample   *model.OnlineEvaluationSample
	event    *model.OutboxEvent
}

func (repository *memoryRepository) FindRun(_ context.Context, userHash, requestID string) (model.AgentRun, error) {
	if repository.run.UserIDHash != userHash || repository.run.RequestID != requestID {
		return model.AgentRun{}, gorm.ErrRecordNotFound
	}
	return repository.run, nil
}

func (repository *memoryRepository) Create(_ context.Context, feedback *model.UserFeedbackEvent, sample *model.OnlineEvaluationSample, event *model.OutboxEvent) (bool, string, string, error) {
	if repository.feedback != nil {
		return false, repository.feedback.ID, repository.feedback.SampleID, nil
	}
	repository.feedback, repository.sample, repository.event = feedback, sample, event
	return true, feedback.ID, sample.ID, nil
}

type recordingMetrics struct{ strategy, feedbackType, result string }

func (metrics *recordingMetrics) RecordFeedback(strategy, feedbackType, result string) {
	metrics.strategy, metrics.feedbackType, metrics.result = strategy, feedbackType, result
}

func TestDownvoteIsOwnerVerifiedSanitizedAndForcedIntoOutbox(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	repository := &memoryRepository{run: model.AgentRun{
		UserIDHash: hash("alice"), RequestID: "request-1", TraceID: "trace-1", SessionID: "session-1",
		Intent: "project_qa", IntentVersion: "intent-v1", Strategy: "rag_fast", StrategyVersion: "rag-v1", PolicyVersion: "policy-v1", Status: "success",
	}}
	metrics := new(recordingMetrics)
	service := NewService(repository, metrics, func() time.Time { return now })
	receipt, err := service.Submit(context.Background(), "alice", "request-1", Submission{
		TraceID: "trace-1", Question: "联系 owner@example.com，password=plain-secret", Answer: "token=answer-secret-value", Confidence: .42, Resolved: false, Feedback: FeedbackDownvote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Created || receipt.SampleRateBasis != 10000 || receipt.Status != "pending" || repository.event.Topic != "gopher.eval.online.v1" {
		t.Fatalf("downvote did not enter the reliable 100%% evaluation path: receipt=%+v event=%+v", receipt, repository.event)
	}
	if repository.feedback.UserHash == "alice" || repository.feedback.RequestHash == "request-1" || repository.sample.UserHash == "alice" {
		t.Fatalf("raw identity was persisted: feedback=%+v sample=%+v", repository.feedback, repository.sample)
	}
	if repository.sample.Question == "联系 owner@example.com，password=plain-secret" || repository.sample.Answer == "token=answer-secret-value" || repository.sample.RedactionCount < 3 {
		t.Fatalf("rated content was not sanitized before enqueue: %+v", repository.sample)
	}
	var reasons []string
	if err := json.Unmarshal([]byte(repository.sample.SampleReasonsJSON), &reasons); err != nil || !contains(reasons, FeedbackDownvote) {
		t.Fatalf("downvote reason missing: %q err=%v", repository.sample.SampleReasonsJSON, err)
	}
	if metrics.strategy != "rag_fast" || metrics.feedbackType != "explicit" || metrics.result != "accepted" {
		t.Fatalf("unexpected feedback metric: %+v", metrics)
	}

	duplicate, err := service.Submit(context.Background(), "alice", "request-1", Submission{
		TraceID: "trace-1", Question: "same", Answer: "same", Feedback: FeedbackDownvote,
	})
	if err != nil || duplicate.Created || duplicate.SampleID != receipt.SampleID || metrics.result != "duplicate" {
		t.Fatalf("feedback idempotency failed: receipt=%+v err=%v metrics=%+v", duplicate, err, metrics)
	}
}

func TestFeedbackRejectsForeignRequestAndTraceMismatch(t *testing.T) {
	repository := &memoryRepository{run: model.AgentRun{UserIDHash: hash("alice"), RequestID: "request-1", TraceID: "trace-1", Strategy: "legacy_chat"}}
	service := NewService(repository, nil, time.Now)
	input := Submission{TraceID: "trace-1", Question: "q", Answer: "a", Feedback: FeedbackDownvote}
	if _, err := service.Submit(context.Background(), "bob", "request-1", input); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("foreign request was not rejected: %v", err)
	}
	input.TraceID = "trace-foreign"
	if _, err := service.Submit(context.Background(), "alice", "request-1", input); !errors.Is(err, ErrTraceMismatch) {
		t.Fatalf("trace mismatch was not rejected: %v", err)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
