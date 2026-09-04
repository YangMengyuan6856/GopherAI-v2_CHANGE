package app

import (
	"GopherAI/internal/contract"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	LegacyIntent          = "legacy"
	LegacyIntentVersion   = "legacy-v0"
	ProjectQAIntent       = "project_qa"
	ExplicitIntentVersion = "explicit-v1"
)

var defaultBudgets = contract.ExecutionBudgets{
	MaxAgents:       1,
	MaxToolCalls:    0,
	MaxIterations:   1,
	MaxInputTokens:  16_000,
	MaxOutputTokens: 4_000,
	TotalTimeout:    90 * time.Second,
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type PolicySelector interface {
	Select(ctx context.Context, request contract.RequestContext, intent contract.IntentResult) (contract.StrategyDecision, error)
}

type StreamEmitter func(contract.StreamEvent) error

type ChatStrategy interface {
	Name() string
	Execute(ctx context.Context, request contract.RequestContext) (contract.AgentResult, error)
	Stream(ctx context.Context, request contract.RequestContext, emit StreamEmitter) (contract.AgentResult, error)
}

type ChatInput struct {
	TraceID           string
	RequestID         string
	UserID            string
	TenantID          string
	SessionID         string
	Question          string
	KnowledgeRequired bool
	Locale            string
	Debug             bool
}

type ChatOutput struct {
	Request  contract.RequestContext
	Intent   contract.IntentResult
	Decision contract.StrategyDecision
	Result   contract.AgentResult
	Trace    contract.TraceEnvelope
}

type Service struct {
	selector   PolicySelector
	strategies map[string]ChatStrategy
	clock      Clock
	ids        IDGenerator
	budgets    contract.ExecutionBudgets
}

func NewService(selector PolicySelector, clock Clock, ids IDGenerator, strategies ...ChatStrategy) (*Service, error) {
	if selector == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("selector, clock and id generator are required")
	}
	registry := make(map[string]ChatStrategy, len(strategies))
	for _, strategy := range strategies {
		if strategy == nil || strategy.Name() == "" {
			return nil, fmt.Errorf("strategy name is required")
		}
		if _, exists := registry[strategy.Name()]; exists {
			return nil, fmt.Errorf("duplicate strategy %q", strategy.Name())
		}
		registry[strategy.Name()] = strategy
	}
	return &Service{selector: selector, strategies: registry, clock: clock, ids: ids, budgets: defaultBudgets}, nil
}

func (service *Service) Chat(ctx context.Context, input ChatInput) (ChatOutput, error) {
	output, strategy, err := service.prepare(ctx, input)
	if err != nil {
		return output, err
	}
	executionContext, cancel := context.WithDeadline(ctx, output.Request.Deadline)
	defer cancel()
	stepStarted := service.clock.Now()
	result, err := strategy.Execute(executionContext, output.Request)
	return service.finish(output, result, "strategy_execute", stepStarted, err)
}

func (service *Service) Stream(ctx context.Context, input ChatInput, emit StreamEmitter) (ChatOutput, error) {
	output, strategy, err := service.prepare(ctx, input)
	if err != nil {
		return output, err
	}
	if emit == nil {
		return output, contract.NewDomainError("STREAM_EMITTER_REQUIRED", contract.ErrorInternal, "流式输出不可用", false, nil)
	}
	executionContext, cancel := context.WithDeadline(ctx, output.Request.Deadline)
	defer cancel()

	enrichedEmitter := func(event contract.StreamEvent) error {
		event.SchemaVersion = contract.SchemaVersion
		event.TraceID = output.Request.TraceID
		event.RequestID = output.Request.RequestID
		event.Intent = output.Intent.Intent
		event.Strategy = output.Decision.StrategyName
		event.PolicyVersion = output.Decision.PolicyVersion
		return emit(event)
	}

	stepStarted := service.clock.Now()
	result, err := strategy.Stream(executionContext, output.Request, enrichedEmitter)
	output, err = service.finish(output, result, "strategy_stream", stepStarted, err)
	if err != nil {
		return output, err
	}
	usage := output.Result.Usage
	if err := enrichedEmitter(contract.StreamEvent{
		Type:           contract.StreamEventFinal,
		SessionID:      output.Result.SessionID,
		Confidence:     output.Result.Confidence,
		NeedsUserInput: output.Result.NeedsUserInput,
		Usage:          &usage,
	}); err != nil {
		return output, contract.NewDomainError("STREAM_WRITE_FAILED", contract.ErrorDependencyUnavailable, "客户端连接已断开", true, err)
	}
	return output, nil
}

func (service *Service) prepare(ctx context.Context, input ChatInput) (ChatOutput, ChatStrategy, error) {
	startedAt := service.clock.Now()
	requestID := input.RequestID
	if requestID == "" {
		requestID = service.ids.NewID()
	}
	traceID := input.TraceID
	if traceID == "" {
		traceID = service.ids.NewID()
	}
	locale := input.Locale
	if locale == "" {
		locale = "zh-CN"
	}
	request := contract.RequestContext{
		TraceID: traceID, RequestID: requestID, UserID: input.UserID, TenantID: input.TenantID,
		SessionID: input.SessionID, Question: input.Question, KnowledgeRequired: input.KnowledgeRequired, Locale: locale, StartedAt: startedAt,
		Deadline: startedAt.Add(service.budgets.TotalTimeout), Debug: input.Debug, Budgets: service.budgets,
	}
	output := ChatOutput{Request: request}
	if err := request.Validate(); err != nil {
		return output, nil, contract.WithTrace(contract.NewDomainError("INVALID_CHAT_REQUEST", contract.ErrorValidation, "请求参数错误", false, err), traceID)
	}

	intent := contract.IntentResult{Intent: LegacyIntent, Confidence: 1, Version: LegacyIntentVersion,
		Stages: []contract.IntentStageResult{{Stage: "fixed", Intent: LegacyIntent, Confidence: 1, ReasonCode: "m2_legacy_adapter"}}}
	if input.KnowledgeRequired {
		intent = contract.IntentResult{Intent: ProjectQAIntent, Confidence: 1, Version: ExplicitIntentVersion,
			Stages: []contract.IntentStageResult{{Stage: "explicit_request", Intent: ProjectQAIntent, Confidence: 1, ReasonCode: "knowledge_required"}}}
	}
	output.Intent = intent
	decision, err := service.selector.Select(ctx, request, intent)
	if err != nil {
		return output, nil, contract.WithTrace(err, traceID)
	}
	if err := decision.Validate(); err != nil {
		return output, nil, contract.WithTrace(contract.NewDomainError("INVALID_STRATEGY_DECISION", contract.ErrorInternal, "策略配置不可用", false, err), traceID)
	}
	request.PolicyVersion = decision.PolicyVersion
	output.Request = request
	output.Decision = decision
	output.Trace = contract.TraceEnvelope{
		SchemaVersion: contract.SchemaVersion, TraceID: traceID, RequestID: requestID, SessionID: input.SessionID,
		Intent: intent, Decision: decision, StartedAt: startedAt,
		Steps: []contract.TraceStep{{Name: "policy_select", StartedAt: startedAt, FinishedAt: service.clock.Now(), Status: "ok",
			Attributes: map[string]any{"reason_code": decision.ReasonCode}}},
	}
	strategy, exists := service.strategies[decision.StrategyName]
	if !exists {
		return output, nil, contract.WithTrace(contract.NewDomainError("STRATEGY_NOT_REGISTERED", contract.ErrorInternal, "策略暂时不可用", false, fmt.Errorf("strategy %s not registered", decision.StrategyName)), traceID)
	}
	return output, strategy, nil
}

func (service *Service) finish(output ChatOutput, result contract.AgentResult, stepName string, startedAt time.Time, err error) (ChatOutput, error) {
	finishedAt := service.clock.Now()
	status := "ok"
	if err != nil {
		status = "error"
	}
	output.Result = result
	output.Trace.SessionID = result.SessionID
	output.Trace.FinishedAt = finishedAt
	output.Trace.Steps = append(output.Trace.Steps, contract.TraceStep{Name: stepName, StartedAt: startedAt, FinishedAt: finishedAt, Status: status})
	if err != nil {
		domainError := contract.WithTrace(err, output.Request.TraceID)
		output.Result.Error = domainError
		output.Trace.Error = domainError
		return output, domainError
	}
	return output, nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string { return uuid.NewString() }
