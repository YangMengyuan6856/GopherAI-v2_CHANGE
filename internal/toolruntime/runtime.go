package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Runtime struct {
	registry *Registry
	auditor  Auditor
	observer Observer
	now      func() time.Time
	cache    *memoryToolCache
	circuits *circuitSet
}

func NewRuntime(registry *Registry, auditor Auditor, observer Observer) (*Runtime, error) {
	if registry == nil {
		return nil, errors.New("tool registry is required")
	}
	if auditor == nil {
		auditor = nopAuditor{}
	}
	if observer == nil {
		observer = nopObserver{}
	}
	return &Runtime{registry: registry, auditor: auditor, observer: observer, now: time.Now, cache: newMemoryToolCache(), circuits: newCircuitSet()}, nil
}

func (runtime *Runtime) Definitions() []Definition { return runtime.registry.Definitions() }

func (runtime *Runtime) Invoke(ctx context.Context, invocation Invocation) ToolMessage {
	startedAt := runtime.now()
	message := ToolMessage{CallID: boundedCallID(invocation.CallID), ToolName: invocation.ToolName, Status: StatusRejected}
	if len(message.CallID) == 0 {
		message.CallID = "missing-call-id"
	}
	tool, found := runtime.registry.Lookup(invocation.ToolName)
	if !found {
		message.ArgsHash = hashBytes(invocation.Arguments)
		message.ErrorCode = ErrorToolNotRegistered
		runtime.finish(ctx, startedAt, invocation, &message, "registry_miss")
		return message
	}
	definition := tool.Definition()
	message.ToolVersion = definition.Version

	arguments, canonical, err := validateArguments(definition.InputSchema, invocation.Arguments)
	if err != nil {
		message.ArgsHash = hashBytes(invocation.Arguments)
		message.Status = StatusInvalidArgs
		message.ErrorCode = ErrorArgumentsInvalid
		runtime.finish(ctx, startedAt, invocation, &message, "invalid_arguments")
		return message
	}
	message.ArgsHash = hashBytes(canonical)
	if !contains(definition.AllowedIntents, invocation.Intent) {
		message.ErrorCode = ErrorIntentDenied
		runtime.finish(ctx, startedAt, invocation, &message, "intent_denied")
		return message
	}
	if !invocation.Principal.Permissions[definition.RequiredPermission] {
		message.ErrorCode = ErrorPermissionDenied
		runtime.finish(ctx, startedAt, invocation, &message, "permission_denied")
		return message
	}
	if !sideEffectAllowed(definition.SideEffect, invocation.AllowedSideEffect) {
		message.ErrorCode = ErrorSideEffectDenied
		runtime.finish(ctx, startedAt, invocation, &message, "side_effect_denied")
		return message
	}
	if invocation.Budget.MaxCalls <= 0 || invocation.Budget.UsedCalls >= invocation.Budget.MaxCalls {
		message.Status = StatusBudgetExceeded
		message.ErrorCode = ErrorBudgetExceeded
		runtime.finish(ctx, startedAt, invocation, &message, "budget_exceeded")
		return message
	}

	runtime.observer.RecordToolValidation(definition.Name, "accepted")
	cacheKey := toolCacheKey(definition, invocation, message.ArgsHash)
	var staleCandidate cachedToolResult
	hasStaleCandidate := false
	cacheOutcomeRecorded := false
	if definition.CacheTTLMS > 0 {
		cached, freshness := runtime.cache.get(cacheKey, runtime.now(), time.Duration(definition.StaleIfErrorMS)*time.Millisecond)
		switch freshness {
		case cacheFresh:
			message.Status, message.Data, message.EvidenceRefs, message.Cached = StatusSuccess, cached.data, cached.evidenceRefs, true
			runtime.observer.RecordToolCache(definition.Name, "hit")
			runtime.finish(ctx, startedAt, invocation, &message, "")
			return message
		case cacheStale:
			staleCandidate, hasStaleCandidate = cached, true
		default:
			runtime.observer.RecordToolCache(definition.Name, "miss")
			cacheOutcomeRecorded = true
		}
	} else {
		runtime.observer.RecordToolCache(definition.Name, "bypass")
		cacheOutcomeRecorded = true
	}
	if allowed, transition := runtime.circuits.allow(definition, runtime.now()); !allowed {
		if hasStaleCandidate && ctx.Err() == nil {
			runtime.applyStaleFallback(definition, &message, staleCandidate, ErrorCircuitOpen)
			runtime.observer.RecordToolCache(definition.Name, "stale_fallback")
			runtime.finish(ctx, startedAt, invocation, &message, "")
			return message
		}
		if hasStaleCandidate && !cacheOutcomeRecorded {
			runtime.observer.RecordToolCache(definition.Name, "miss")
		}
		message.Status, message.ErrorCode, message.Retryable = StatusError, ErrorCircuitOpen, true
		runtime.finish(ctx, startedAt, invocation, &message, "")
		return message
	} else if transition != "" {
		runtime.observer.SetToolCircuitState(definition.Name, transition)
	}
	callContext, cancel := context.WithTimeout(ctx, time.Duration(definition.TimeoutMS)*time.Millisecond)
	defer cancel()
	callContext = withExecutionPrincipal(callContext, invocation.Principal)
	var output Output
	var executeErr error
	for attempt := 1; attempt <= definition.RetryMaxAttempts; attempt++ {
		output, executeErr = tool.Execute(callContext, arguments)
		if executeErr == nil {
			break
		}
		if callContext.Err() != nil || !definition.Idempotent || !output.Retryable || attempt == definition.RetryMaxAttempts {
			break
		}
		runtime.observer.RecordToolRetry(definition.Name, "temporary_error")
	}
	if executeErr != nil {
		switch {
		case errors.Is(callContext.Err(), context.DeadlineExceeded), errors.Is(executeErr, context.DeadlineExceeded):
			message.Status, message.ErrorCode, message.Retryable = StatusTimeout, ErrorTimeout, true
			runtime.observer.RecordToolCancellation(definition.Name, "timeout")
		case errors.Is(callContext.Err(), context.Canceled), errors.Is(executeErr, context.Canceled):
			message.Status, message.ErrorCode = StatusCancelled, ErrorCancelled
			runtime.observer.RecordToolCancellation(definition.Name, "request_cancelled")
		default:
			if code, retryable, ok := executionErrorDetails(executeErr); ok {
				message.Status, message.ErrorCode, message.Retryable = StatusError, code, retryable
			} else {
				message.Status, message.ErrorCode, message.Retryable = StatusError, ErrorExecutionFailed, output.Retryable
			}
		}
		if message.Status != StatusCancelled {
			if transition := runtime.circuits.failure(definition, runtime.now()); transition != "" {
				runtime.observer.SetToolCircuitState(definition.Name, transition)
			}
		}
		// Never convert caller cancellation/deadline into apparent success. Stale
		// evidence is only a dependency fallback after all governance gates pass.
		if hasStaleCandidate && ctx.Err() == nil && message.Status != StatusCancelled {
			runtime.applyStaleFallback(definition, &message, staleCandidate, message.ErrorCode)
			runtime.observer.RecordToolCache(definition.Name, "stale_fallback")
			runtime.finish(ctx, startedAt, invocation, &message, "")
			return message
		}
		if hasStaleCandidate && !cacheOutcomeRecorded {
			runtime.observer.RecordToolCache(definition.Name, "miss")
		}
		runtime.finish(ctx, startedAt, invocation, &message, "")
		return message
	}
	encoded, err := json.Marshal(output.Data)
	if err != nil {
		message.Status, message.ErrorCode = StatusError, ErrorExecutionFailed
		if transition := runtime.circuits.failure(definition, runtime.now()); transition != "" {
			runtime.observer.SetToolCircuitState(definition.Name, transition)
		}
		if hasStaleCandidate && !cacheOutcomeRecorded {
			runtime.observer.RecordToolCache(definition.Name, "miss")
		}
		runtime.finish(ctx, startedAt, invocation, &message, "")
		return message
	}
	if len(encoded) > definition.MaxResultBytes {
		message.Status, message.ErrorCode, message.Truncated = StatusError, ErrorResultTooLarge, true
		message.Data, _ = json.Marshal(map[string]any{"original_bytes": len(encoded), "max_result_bytes": definition.MaxResultBytes})
		if transition := runtime.circuits.failure(definition, runtime.now()); transition != "" {
			runtime.observer.SetToolCircuitState(definition.Name, transition)
		}
		if hasStaleCandidate && !cacheOutcomeRecorded {
			runtime.observer.RecordToolCache(definition.Name, "miss")
		}
		runtime.finish(ctx, startedAt, invocation, &message, "")
		return message
	}
	message.Status = StatusSuccess
	message.Data = encoded
	message.EvidenceRefs = append([]string(nil), output.EvidenceRefs...)
	if transition := runtime.circuits.success(definition); transition != "" {
		runtime.observer.SetToolCircuitState(definition.Name, transition)
	}
	if definition.CacheTTLMS > 0 {
		if hasStaleCandidate && !cacheOutcomeRecorded {
			runtime.observer.RecordToolCache(definition.Name, "miss")
		}
		runtime.cache.put(cacheKey, cachedToolResult{data: encoded, evidenceRefs: message.EvidenceRefs}, runtime.now().Add(time.Duration(definition.CacheTTLMS)*time.Millisecond))
	}
	runtime.finish(ctx, startedAt, invocation, &message, "")
	return message
}

func (runtime *Runtime) applyStaleFallback(definition Definition, message *ToolMessage, cached cachedToolResult, reason string) {
	message.Status = StatusSuccess
	message.Data = cached.data
	message.EvidenceRefs = append([]string(nil), cached.evidenceRefs...)
	message.EvidenceRefs = append(message.EvidenceRefs, "tool-cache-stale:"+definition.Name+"@"+definition.Version)
	message.ErrorCode = ""
	message.Retryable = false
	message.Cached = true
	message.Stale = true
	message.DegradedReason = reason
}

func (runtime *Runtime) finish(ctx context.Context, startedAt time.Time, invocation Invocation, message *ToolMessage, validation string) {
	duration := runtime.now().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	message.LatencyMS = duration.Milliseconds()
	if validation != "" {
		runtime.observer.RecordToolValidation(boundedMetricTool(message.ToolName), validation)
	}
	runtime.observer.RecordToolCall(boundedMetricTool(message.ToolName), invocation.Strategy, message.Status, duration)
	// Audit uses a detached, tightly bounded context so a cancelled request still
	// leaves a sanitized record of the attempted invocation.
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if err := runtime.auditor.Record(auditContext, invocation, *message); err != nil {
		runtime.observer.RecordToolAuditFailure(boundedMetricTool(message.ToolName))
	}
}

func sideEffectAllowed(required SideEffect, allowed SideEffect) bool {
	rank := map[SideEffect]int{SideEffectReadOnly: 1, SideEffectInternalWrite: 2, SideEffectExternalWrite: 3}
	return rank[required] > 0 && rank[required] <= rank[allowed]
}

func boundedCallID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func boundedMetricTool(value string) string {
	if toolNamePattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
