package observability

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/model"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	statusSuccess = "success"
	statusError   = "error"
	unknownLabel  = "unknown"
)

type RunRepository interface {
	Create(run *model.AgentRun) error
}

type mysqlRunRepository struct{}

func (mysqlRunRepository) Create(run *model.AgentRun) error {
	if mysql.DB == nil {
		return gorm.ErrInvalidDB
	}
	return mysql.DB.Create(run).Error
}

type Recorder struct {
	repository RunRepository
	metrics    *Metrics
	logger     *log.Logger
}

func NewRecorder(repository RunRepository, metrics *Metrics, logger *log.Logger) *Recorder {
	return &Recorder{repository: repository, metrics: metrics, logger: logger}
}

func NewDefaultRecorder() *Recorder {
	return NewRecorder(mysqlRunRepository{}, DefaultMetrics(), log.Default())
}

func (recorder *Recorder) Record(output app.ChatOutput, requestError error) {
	if recorder == nil {
		return
	}
	run := buildRun(output, requestError)
	if recorder.metrics != nil {
		recorder.metrics.record(run, output.Intent.Confidence)
	}
	persistenceStatus := "disabled"
	if recorder.repository != nil {
		persistenceStatus = "stored"
		if err := recorder.repository.Create(&run); err != nil {
			persistenceStatus = "failed"
			if recorder.metrics != nil {
				recorder.metrics.persistFailures.WithLabelValues("agent_run").Inc()
			}
		}
	}
	recorder.writeLog(run, persistenceStatus)
}

func (metrics *Metrics) record(run model.AgentRun, confidence float64) {
	intent := boundedLabel(run.Intent)
	strategy := boundedLabel(run.Strategy)
	stage := boundedLabel(run.FinalIntentStage)
	status := boundedStatus(run.Status)
	duration := float64(run.DurationMicros) / float64(time.Second/time.Microsecond)
	metrics.requests.WithLabelValues(intent, strategy, status).Inc()
	metrics.requestDuration.WithLabelValues(intent, strategy).Observe(duration)
	metrics.intentDecisions.WithLabelValues(intent, stage, status).Inc()
	metrics.intentConfidence.WithLabelValues(intent, stage).Observe(clampConfidence(confidence))
	metrics.agentRuns.WithLabelValues(strategy, strategy, status).Inc()
	metrics.agentDuration.WithLabelValues(strategy, strategy).Observe(duration)
}

func (recorder *Recorder) writeLog(run model.AgentRun, persistenceStatus string) {
	if recorder.logger == nil {
		return
	}
	fields := map[string]any{
		"event": "agent_run_completed", "trace_id": run.TraceID, "request_id": run.RequestID,
		"session_id": run.SessionID, "user_id_hash": run.UserIDHash, "intent": run.Intent,
		"intent_version": run.IntentVersion, "final_intent_stage": run.FinalIntentStage,
		"strategy": run.Strategy, "strategy_version": run.StrategyVersion,
		"policy_version": run.PolicyVersion, "status": run.Status,
		"duration_micros": run.DurationMicros, "error_code": run.ErrorCode,
		"persistence_status": persistenceStatus,
	}
	if encoded, err := json.Marshal(fields); err == nil {
		recorder.logger.Print(string(encoded))
	}
}

func buildRun(output app.ChatOutput, requestError error) model.AgentRun {
	startedAt := output.Trace.StartedAt
	if startedAt.IsZero() {
		startedAt = output.Request.StartedAt
	}
	finishedAt := output.Trace.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	duration := finishedAt.Sub(startedAt)
	if startedAt.IsZero() || duration < 0 {
		duration = 0
	}
	status := statusSuccess
	errorCode := ""
	if requestError != nil || output.Result.Error != nil || output.Trace.Error != nil {
		status = statusError
		var domainError *contract.DomainError
		if requestError != nil {
			domainError = contract.WithTrace(requestError, output.Request.TraceID)
		}
		if domainError == nil {
			domainError = output.Result.Error
		}
		if domainError == nil {
			domainError = output.Trace.Error
		}
		if domainError != nil {
			errorCode = domainError.Code
		}
	}
	traceJSON, _ := json.Marshal(output.Trace)
	return model.AgentRun{
		TraceID: output.Request.TraceID, RequestID: output.Request.RequestID,
		SessionID: output.Result.SessionID, UserIDHash: HashUserID(output.Request.UserID),
		Intent: output.Intent.Intent, IntentVersion: output.Intent.Version,
		FinalIntentStage: finalIntentStage(output.Intent), Strategy: output.Decision.StrategyName,
		StrategyVersion: output.Decision.StrategyVersion, PolicyVersion: output.Decision.PolicyVersion,
		Status: status, DurationMicros: duration.Microseconds(), InputTokens: output.Result.Usage.InputTokens,
		OutputTokens: output.Result.Usage.OutputTokens, CostMicros: output.Result.Usage.CostMicros,
		EvidenceCount: len(output.Result.Evidence), ToolCallCount: len(output.Result.ToolCalls),
		ErrorCode: errorCode, TraceEnvelopeJSON: string(traceJSON), StartedAt: startedAt, FinishedAt: finishedAt,
	}
}

func finalIntentStage(intent contract.IntentResult) string {
	if len(intent.Stages) == 0 {
		return unknownLabel
	}
	return intent.Stages[len(intent.Stages)-1].Stage
}

func HashUserID(userID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userID)))
	return hex.EncodeToString(sum[:])
}

func boundedLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return unknownLabel
	}
	return value
}

func boundedStatus(status string) string {
	if status == statusSuccess {
		return statusSuccess
	}
	return statusError
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
