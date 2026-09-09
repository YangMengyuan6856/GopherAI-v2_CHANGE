package onlineeval

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/jobqueue"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	DefaultMaximumAttempts = 3
	ErrorEventInvalid      = "ONLINE_EVAL_EVENT_INVALID"
	ErrorSampleNotFound    = "ONLINE_EVAL_SAMPLE_NOT_FOUND"
	ErrorRepository        = "ONLINE_EVAL_REPOSITORY_FAILED"
	ErrorJudgeInput        = "ONLINE_EVAL_INPUT_UNAVAILABLE"
	ErrorJudgeFailed       = "ONLINE_EVAL_JUDGE_FAILED"
)

type Judge interface {
	Judge(context.Context, evaluation.JudgeInput) (evaluation.JudgeResult, error)
}

type EvaluationObserver interface {
	RecordOnlineEvaluation(string, evaluation.JudgeScores)
	RecordOnlineEvaluationFailure(string, string)
}

type Processor struct {
	repository Repository
	judge      Judge
	observer   EvaluationObserver
	clock      func() time.Time
}

type Consumer struct {
	processor   *Processor
	maxAttempts int
}

func (consumer *Consumer) PruneExpired(ctx context.Context, now time.Time) (int64, error) {
	if consumer == nil || consumer.processor == nil {
		return 0, errors.New("online evaluation consumer is unavailable")
	}
	return consumer.processor.repository.PruneExpired(ctx, now.UTC())
}

func NewProcessor(repository Repository, judge Judge, observer EvaluationObserver, clock func() time.Time) (*Processor, error) {
	if repository == nil || judge == nil {
		return nil, errors.New("online evaluation repository and judge are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Processor{repository: repository, judge: judge, observer: observer, clock: clock}, nil
}

func NewConsumer(processor *Processor, maxAttempts int) (*Consumer, error) {
	if processor == nil || maxAttempts <= 0 {
		return nil, errors.New("online evaluation processor and positive maximum attempts are required")
	}
	return &Consumer{processor: processor, maxAttempts: maxAttempts}, nil
}

func (processor *Processor) Process(ctx context.Context, envelope jobqueue.Envelope) (string, error) {
	if envelope.EventType != EventType || envelope.AggregateID == "" || envelope.AggregateVersion != 1 {
		return ErrorEventInvalid, errors.New("online evaluation envelope identity is invalid")
	}
	var payload eventPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.SchemaVersion != SchemaVersion || payload.SampleID != envelope.AggregateID || payload.PayloadHash == "" {
		return ErrorEventInvalid, errors.New("online evaluation payload is invalid")
	}
	sample, err := processor.repository.Get(ctx, payload.SampleID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrorSampleNotFound, err
		}
		return ErrorRepository, err
	}
	if sample.PayloadHash != payload.PayloadHash || sample.SchemaVersion != SchemaVersion {
		return ErrorEventInvalid, errors.New("online evaluation payload integrity check failed")
	}
	if sample.Status == StatusCompleted || sample.Status == StatusJudgeFailed {
		return "", nil
	}
	now := processor.clock().UTC()
	claimed, err := processor.repository.MarkEvaluating(ctx, sample.ID, envelope.Attempt+1, now)
	if err != nil {
		return ErrorRepository, err
	}
	if !claimed {
		return "", nil
	}
	if sample.Simulation {
		result := EvaluationResult{
			AdapterVersion: "deterministic-acceptance-v1", PromptVersion: "deterministic-acceptance-v1", ModelVersion: "no-model-call",
			Relevance: 1, Completeness: 1, Helpfulness: 1, Groundedness: 1, Safety: 1, Overall: 1,
			ResultJSON: `{"simulation":true,"production_metrics_emitted":false}`,
		}
		if err := processor.repository.Complete(ctx, sample.ID, result, now); err != nil {
			return ErrorRepository, err
		}
		return "", nil
	}
	if sample.Question == "[CONTENT_REDACTED]" || sample.Answer == "[CONTENT_REDACTED]" {
		if err := processor.repository.Fail(ctx, sample.ID, ErrorJudgeInput, envelope.Attempt+1, now); err != nil {
			return ErrorRepository, err
		}
		processor.recordFailure(sample.Strategy, "input_unavailable")
		return "", nil
	}
	var evidence []contract.Evidence
	if err := json.Unmarshal([]byte(sample.EvidenceJSON), &evidence); err != nil {
		if persistErr := processor.repository.Fail(ctx, sample.ID, ErrorJudgeInput, envelope.Attempt+1, now); persistErr != nil {
			return ErrorRepository, persistErr
		}
		processor.recordFailure(sample.Strategy, "input_unavailable")
		return "", nil
	}
	judgeResult, judgeErr := processor.judge.Judge(ctx, evaluation.JudgeInput{
		TaskType: sample.Intent, Question: sample.Question, Answer: sample.Answer, Evidence: evidence,
	})
	if judgeErr != nil {
		code := ErrorJudgeFailed
		if strings.TrimSpace(judgeResult.ErrorCode) != "" {
			code = boundedToken(judgeResult.ErrorCode, 64, ErrorJudgeFailed)
		}
		if err := processor.repository.Fail(ctx, sample.ID, code, envelope.Attempt+1, now); err != nil {
			return ErrorRepository, err
		}
		processor.recordFailure(sample.Strategy, "judge_failed")
		return "", nil
	}
	encoded, err := json.Marshal(judgeResult)
	if err != nil {
		return ErrorRepository, err
	}
	result := EvaluationResult{
		AdapterVersion: judgeResult.AdapterVersion, PromptVersion: judgeResult.PromptVersion, ModelVersion: judgeResult.ModelVersion,
		Relevance: judgeResult.Scores.Relevance, Completeness: judgeResult.Scores.Completeness, Helpfulness: judgeResult.Scores.Helpfulness,
		Groundedness: judgeResult.Scores.Groundedness, Safety: judgeResult.Scores.Safety, Overall: judgeResult.Overall, ResultJSON: string(encoded),
	}
	if err := processor.repository.Complete(ctx, sample.ID, result, now); err != nil {
		return ErrorRepository, err
	}
	if processor.observer != nil {
		processor.observer.RecordOnlineEvaluation(sample.Strategy, judgeResult.Scores)
	}
	return "", nil
}

func (processor *Processor) recordFailure(strategy, reason string) {
	if processor.observer != nil {
		processor.observer.RecordOnlineEvaluationFailure(strategy, reason)
	}
}

func (processor *Processor) Exhaust(ctx context.Context, envelope jobqueue.Envelope, code string) error {
	return processor.repository.MarkDead(ctx, envelope.AggregateID, code, envelope.Attempt+1, processor.clock().UTC())
}

func (consumer *Consumer) Handle(ctx context.Context, body []byte) jobqueue.Result {
	var envelope jobqueue.Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		consumer.processor.recordFailure("unknown", "event_invalid")
		return jobqueue.Result{Action: jobqueue.ActionDead, Body: body, Code: ErrorEventInvalid}
	}
	code, err := consumer.processor.Process(ctx, envelope)
	if err == nil {
		writeConsumerLog(envelope, "ack", "")
		return jobqueue.Result{Action: jobqueue.ActionAck}
	}
	retryable := code == ErrorRepository
	if retryable && envelope.Attempt+1 < consumer.maxAttempts {
		envelope.Attempt++
		retryBody, marshalErr := json.Marshal(envelope)
		if marshalErr == nil {
			writeConsumerLog(envelope, "retry", code)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: code}
		}
	}
	if retryable {
		if exhaustErr := consumer.processor.Exhaust(ctx, envelope, code); exhaustErr != nil {
			envelope.Attempt++
			retryBody, _ := json.Marshal(envelope)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: ErrorRepository}
		}
	}
	consumer.processor.recordFailure("unknown", boundedFailureReason(code))
	deadBody, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		deadBody = body
	}
	writeConsumerLog(envelope, "dead", code)
	return jobqueue.Result{Action: jobqueue.ActionDead, Body: deadBody, Code: code}
}

func boundedFailureReason(code string) string {
	switch code {
	case ErrorEventInvalid:
		return "event_invalid"
	case ErrorSampleNotFound:
		return "sample_not_found"
	case ErrorRepository:
		return "repository_failed"
	default:
		return "unknown"
	}
}

func writeConsumerLog(envelope jobqueue.Envelope, status, code string) {
	record := map[string]any{"event": "online_evaluation_job", "event_id": envelope.EventID, "sample_id": envelope.AggregateID, "attempt": envelope.Attempt, "status": status}
	if code != "" {
		record["error_code"] = code
	}
	if encoded, err := json.Marshal(record); err == nil {
		log.Print(string(encoded))
	}
}
