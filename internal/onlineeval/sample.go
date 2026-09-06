package onlineeval

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/diagnostic"
	"GopherAI/model"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SchemaVersion       = "online-evaluation-sample-v1"
	SamplerVersion      = "risk-stratified-sampler-v1"
	EvaluatorVersion    = "async-llm-judge-v1"
	EventType           = "online-evaluation.requested"
	Topic               = "gopher.eval.online.v1"
	StatusPending       = "pending"
	StatusEvaluating    = "evaluating"
	StatusCompleted     = "completed"
	StatusJudgeFailed   = "judge_failed"
	StatusDead          = "dead"
	TrafficStable       = "stable"
	TrafficCanary       = "canary"
	TrafficProbing      = "probing"
	StableRateBasis     = 400
	CanaryRateBasis     = 2000
	ProbingRateBasis    = 5000
	ForcedRateBasis     = 10000
	BucketCount         = 10000
	LowConfidenceCutoff = 0.60
	RetentionDays       = 7
)

type SamplingDecision struct {
	Selected     bool     `json:"selected"`
	TrafficClass string   `json:"traffic_class"`
	RateBasis    int      `json:"sample_rate_basis"`
	Bucket       int      `json:"sample_bucket"`
	Reasons      []string `json:"reasons"`
	Forced       bool     `json:"forced"`
}

type eventPayload struct {
	SchemaVersion string `json:"schema_version"`
	SampleID      string `json:"sample_id"`
	PayloadHash   string `json:"payload_hash"`
}

func Decide(output app.ChatOutput, requestErr error) SamplingDecision {
	return decide(output, requestErr, "")
}

func decide(output app.ChatOutput, requestErr error, forceReason string) SamplingDecision {
	reasons := make([]string, 0, 4)
	if forceReason != "" {
		reasons = append(reasons, forceReason)
	}
	var domainError *contract.DomainError
	if requestErr != nil {
		reasons = append(reasons, "request_error")
		if errors.As(requestErr, &domainError) {
			switch domainError.Category {
			case contract.ErrorBudgetExceeded:
				reasons = append(reasons, "budget_exceeded")
			case contract.ErrorEvidenceInsufficient:
				reasons = append(reasons, "evidence_gate_failed")
			}
		}
	}
	if output.Result.Error != nil {
		switch output.Result.Error.Category {
		case contract.ErrorBudgetExceeded:
			reasons = append(reasons, "budget_exceeded")
		case contract.ErrorEvidenceInsufficient:
			reasons = append(reasons, "evidence_gate_failed")
		}
	}
	if output.Result.Confidence > 0 && output.Result.Confidence < LowConfidenceCutoff {
		reasons = append(reasons, "low_confidence")
	}
	for _, call := range output.Result.ToolCalls {
		if call.ErrorCode != "" || (call.Status != "" && call.Status != "success") {
			reasons = append(reasons, "tool_failure")
			break
		}
	}
	strategy := strings.ToLower(strings.TrimSpace(output.Decision.StrategyName))
	if strategy != "" && strategy != "legacy_chat" && !output.Result.Resolved {
		reasons = append(reasons, "unresolved")
	}
	if strings.HasPrefix(strategy, "rag_") && len(output.Result.Evidence) == 0 {
		reasons = append(reasons, "evidence_gate_failed")
	}
	reasons = uniqueReasons(reasons)

	trafficClass, rate := trafficRate(output.Decision)
	bucket := stableBucket(output.Request.RequestID, output.Request.TraceID, output.Request.Question)
	forced := len(reasons) > 0
	if forced {
		rate = ForcedRateBasis
	}
	if len(reasons) == 0 {
		reasons = []string{trafficClass + "_rate"}
	}
	return SamplingDecision{Selected: forced || bucket < rate, TrafficClass: trafficClass, RateBasis: rate, Bucket: bucket, Reasons: reasons, Forced: forced}
}

func BuildSample(output app.ChatOutput, requestErr error, decision SamplingDecision, now time.Time, simulation bool) (model.OnlineEvaluationSample, model.OutboxEvent, error) {
	if !decision.Selected {
		return model.OnlineEvaluationSample{}, model.OutboxEvent{}, errors.New("online evaluation sample was not selected")
	}
	now = now.UTC()
	question, questionRedactions := sanitize(output.Request.Question, 4000)
	answer, answerRedactions := sanitize(output.Result.Answer, 8000)
	evidence, evidenceRedactions := sanitizeEvidence(output.Result.Evidence, hashValue(output.Request.TenantID))
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return model.OnlineEvaluationSample{}, model.OutboxEvent{}, fmt.Errorf("encode online evaluation evidence: %w", err)
	}
	reasonsJSON, err := json.Marshal(decision.Reasons)
	if err != nil {
		return model.OnlineEvaluationSample{}, model.OutboxEvent{}, fmt.Errorf("encode online evaluation reasons: %w", err)
	}
	sampleID, eventID := uuid.NewString(), uuid.NewString()
	errorCode := ""
	if output.Result.Error != nil {
		errorCode = boundedToken(output.Result.Error.Code, 64, "result_error")
	} else if requestErr != nil {
		var domainError *contract.DomainError
		if errors.As(requestErr, &domainError) {
			errorCode = boundedToken(domainError.Code, 64, "request_error")
		} else {
			errorCode = "request_error"
		}
	}
	payloadHash := hashValue(strings.Join([]string{question, answer, string(evidenceJSON), output.Decision.PolicyVersion, strings.Join(decision.Reasons, ",")}, "\x00"))
	sample := model.OnlineEvaluationSample{
		ID: sampleID, EventID: eventID, SchemaVersion: SchemaVersion, SamplerVersion: SamplerVersion, EvaluatorVersion: EvaluatorVersion,
		RequestHash: hashValue(output.Request.RequestID), TraceHash: hashValue(output.Request.TraceID), UserHash: hashValue(output.Request.UserID), TenantHash: hashValue(output.Request.TenantID),
		Intent: boundedToken(output.Intent.Intent, 64, "unknown"), IntentVersion: boundedToken(output.Intent.Version, 64, "unknown"),
		Strategy: boundedToken(output.Decision.StrategyName, 64, "unknown"), StrategyVersion: boundedToken(output.Decision.StrategyVersion, 64, "unknown"), PolicyVersion: boundedToken(output.Decision.PolicyVersion, 64, "unknown"),
		TrafficClass: decision.TrafficClass, SampleRateBasis: decision.RateBasis, SampleBucket: decision.Bucket, SampleReasonsJSON: string(reasonsJSON),
		Question: question, Answer: answer, EvidenceJSON: string(evidenceJSON), RedactionCount: questionRedactions + answerRedactions + evidenceRedactions, PayloadHash: payloadHash,
		ResponseConfidence: output.Result.Confidence, Resolved: output.Result.Resolved, ResponseErrorCode: errorCode, Status: StatusPending,
		Simulation: simulation, ExpiresAt: now.AddDate(0, 0, RetentionDays), CreatedAt: now, UpdatedAt: now,
	}
	payload, err := json.Marshal(eventPayload{SchemaVersion: SchemaVersion, SampleID: sampleID, PayloadHash: payloadHash})
	if err != nil {
		return model.OnlineEvaluationSample{}, model.OutboxEvent{}, fmt.Errorf("encode online evaluation event: %w", err)
	}
	event := model.OutboxEvent{
		ID: eventID, Topic: Topic, EventType: EventType, TraceID: sampleID, TenantID: sample.TenantHash,
		AggregateID: sampleID, AggregateVersion: 1, PayloadJSON: string(payload), Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	return sample, event, nil
}

func trafficRate(decision contract.StrategyDecision) (string, int) {
	identity := strings.ToLower(strings.Join([]string{decision.PolicyVersion, decision.ReasonCode, decision.ExperimentBucket}, " "))
	if strings.Contains(identity, "prob") || strings.Contains(identity, "explor") {
		return TrafficProbing, ProbingRateBasis
	}
	if strings.Contains(identity, "canary") || strings.Contains(identity, "candidate") {
		return TrafficCanary, CanaryRateBasis
	}
	return TrafficStable, StableRateBasis
}

func stableBucket(values ...string) int {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return int(uint16(sum[0])<<8|uint16(sum[1])) % BucketCount
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func sanitize(value string, maximum int) (string, int) {
	cleaned, redactions, err := diagnostic.SanitizeFreeText(value, maximum)
	if err != nil {
		return "[CONTENT_REDACTED]", redactions
	}
	return cleaned, redactions
}

func sanitizeEvidence(input []contract.Evidence, tenantHash string) ([]contract.Evidence, int) {
	if len(input) > 20 {
		input = input[:20]
	}
	result := make([]contract.Evidence, 0, len(input))
	redactions := 0
	seen := make(map[string]struct{}, len(input))
	for ordinal, item := range input {
		id := fmt.Sprintf("evidence-%d-%s", ordinal+1, hashValue(item.ID)[:12])
		if _, exists := seen[id]; exists {
			id = fmt.Sprintf("%s-%d", id, ordinal+1)
		}
		seen[id] = struct{}{}
		title, count := sanitizeOptional(item.Title, 500)
		redactions += count
		section, count := sanitizeOptional(item.Section, 500)
		redactions += count
		content, count := sanitizeOptional(item.Content, 4000)
		redactions += count
		parent, count := sanitizeOptional(item.ParentContext, 4000)
		redactions += count
		parentSection, count := sanitizeOptional(item.ParentSection, 500)
		redactions += count
		sourceVersion, count := sanitizeOptional(item.SourceVersion, 128)
		redactions += count
		sourceRevision, count := sanitizeOptional(item.SourceRevision, 128)
		redactions += count
		parentEvidenceID := ""
		if strings.TrimSpace(item.ParentEvidenceID) != "" {
			parentEvidenceID = "parent-" + hashValue(item.ParentEvidenceID)[:12]
		}
		result = append(result, contract.Evidence{
			ID: id, Kind: boundedToken(item.Kind, 64, "knowledge"), TenantID: tenantHash, SourceID: hashValue(item.SourceID), SourceVersion: sourceVersion,
			Title: title, Section: section, LineStart: item.LineStart, LineEnd: item.LineEnd, Content: content, Score: item.Score, Retrieval: boundedToken(item.Retrieval, 64, "unknown"),
			ContentHash: item.ContentHash, ParentEvidenceID: parentEvidenceID, ParentContext: parent, ParentSection: parentSection, ParentLineStart: item.ParentLineStart, ParentLineEnd: item.ParentLineEnd,
			SourceKind: boundedToken(item.SourceKind, 32, ""), SourceRevision: sourceRevision, Authority: item.Authority, EffectiveAt: item.EffectiveAt, ExpiredAt: item.ExpiredAt, SupersedesVersion: item.SupersedesVersion,
		})
	}
	return result, redactions
}

func sanitizeOptional(value string, maximum int) (string, int) {
	if strings.TrimSpace(value) == "" {
		return "", 0
	}
	return sanitize(value, maximum)
}

func boundedToken(value string, maximum int, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > maximum {
		value = string(runes[:maximum])
	}
	return value
}

func uniqueReasons(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, item := range input {
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
