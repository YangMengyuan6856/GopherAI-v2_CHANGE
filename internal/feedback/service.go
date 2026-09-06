package feedback

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/onlineeval"
	"GopherAI/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	SchemaVersion        = "user-feedback-v1"
	FeedbackDownvote     = "user_downvote"
	MaximumQuestionRunes = 4000
	MaximumAnswerRunes   = 8000
)

var (
	ErrInvalidFeedback = errors.New("invalid feedback request")
	ErrRunNotFound     = errors.New("rated request was not found")
	ErrTraceMismatch   = errors.New("feedback trace does not match request")
)

type Metrics interface {
	RecordFeedback(strategy, feedbackType, result string)
}

type Submission struct {
	TraceID    string
	Question   string
	Answer     string
	Confidence float64
	Resolved   bool
	Feedback   string
}

type Receipt struct {
	SchemaVersion   string    `json:"schema_version"`
	FeedbackID      string    `json:"feedback_id"`
	SampleID        string    `json:"sample_id"`
	Created         bool      `json:"created"`
	Status          string    `json:"status"`
	FeedbackType    string    `json:"feedback_type"`
	SampleRateBasis int       `json:"sample_rate_basis"`
	Queue           string    `json:"queue"`
	Strategy        string    `json:"strategy"`
	PolicyVersion   string    `json:"policy_version"`
	CreatedAt       time.Time `json:"created_at"`
	Guardrails      []string  `json:"guardrails"`
}

type Service struct {
	repository Repository
	metrics    Metrics
	clock      func() time.Time
}

func NewService(repository Repository, metrics Metrics, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{repository: repository, metrics: metrics, clock: clock}
}

func (service *Service) Submit(ctx context.Context, userID, requestID string, input Submission) (Receipt, error) {
	if service == nil || service.repository == nil {
		return Receipt{}, gorm.ErrInvalidDB
	}
	userID, requestID = strings.TrimSpace(userID), strings.TrimSpace(requestID)
	input.TraceID, input.Question, input.Answer, input.Feedback = strings.TrimSpace(input.TraceID), strings.TrimSpace(input.Question), strings.TrimSpace(input.Answer), strings.TrimSpace(input.Feedback)
	if userID == "" || requestID == "" || len([]rune(requestID)) > 128 || input.TraceID == "" || input.Question == "" || input.Answer == "" || input.Feedback != FeedbackDownvote || len([]rune(input.Question)) > MaximumQuestionRunes || len([]rune(input.Answer)) > MaximumAnswerRunes || input.Confidence < 0 || input.Confidence > 1 {
		service.record("unknown", "rejected")
		return Receipt{}, ErrInvalidFeedback
	}
	userHash := hash(userID)
	run, err := service.repository.FindRun(ctx, userHash, requestID)
	if err != nil {
		service.record("unknown", "rejected")
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Receipt{}, ErrRunNotFound
		}
		return Receipt{}, err
	}
	if run.TraceID != input.TraceID {
		service.record(run.Strategy, "rejected")
		return Receipt{}, ErrTraceMismatch
	}
	if run.Status != "success" {
		service.record(run.Strategy, "rejected")
		return Receipt{}, ErrRunNotFound
	}
	now := service.clock().UTC()
	output := app.ChatOutput{
		Request:  contract.RequestContext{TraceID: run.TraceID, RequestID: run.RequestID, UserID: userID, TenantID: userID, SessionID: run.SessionID, Question: input.Question},
		Intent:   contract.IntentResult{Intent: run.Intent, Version: run.IntentVersion},
		Decision: contract.StrategyDecision{StrategyName: run.Strategy, StrategyVersion: run.StrategyVersion, PolicyVersion: run.PolicyVersion},
		Result:   contract.AgentResult{SessionID: run.SessionID, Answer: input.Answer, Confidence: input.Confidence, Resolved: input.Resolved},
	}
	sample, event, decision, err := onlineeval.BuildFeedbackSample(output, input.Feedback, now)
	if err != nil {
		service.record(run.Strategy, "error")
		return Receipt{}, err
	}
	feedbackID := uuid.NewString()
	record := model.UserFeedbackEvent{
		ID: feedbackID, UserHash: userHash, RequestHash: hash(run.RequestID), FeedbackType: input.Feedback,
		TenantHash: userHash, TraceHash: hash(run.TraceID), SampleID: sample.ID, Strategy: run.Strategy,
		PolicyVersion: run.PolicyVersion, PayloadHash: hash(sample.PayloadHash + "\x00" + input.Feedback), CreatedAt: now,
	}
	created, feedbackID, sampleID, err := service.repository.Create(ctx, &record, &sample, &event)
	if err != nil {
		service.record(run.Strategy, "error")
		return Receipt{}, err
	}
	result := "duplicate"
	if created {
		result = "accepted"
	}
	service.record(run.Strategy, result)
	return Receipt{
		SchemaVersion: SchemaVersion, FeedbackID: feedbackID, SampleID: sampleID, Created: created,
		Status: onlineeval.StatusPending, FeedbackType: input.Feedback, SampleRateBasis: decision.RateBasis,
		Queue: onlineeval.Topic, Strategy: run.Strategy, PolicyVersion: run.PolicyVersion, CreatedAt: now,
		Guardrails: []string{"request_owner_verified", "idempotent_feedback", "content_redacted_before_queue", "no_active_policy_write"},
	}, nil
}

func (service *Service) record(strategy, result string) {
	if service.metrics != nil {
		service.metrics.RecordFeedback(strategy, "explicit", result)
	}
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
