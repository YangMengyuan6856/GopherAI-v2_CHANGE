package toolagent

import (
	"context"
	"errors"
	"strconv"

	"GopherAI/internal/toolruntime"
)

type RuntimeInvoker interface {
	Invoke(context.Context, toolruntime.Invocation) toolruntime.ToolMessage
}

type RepairRecord struct {
	CallIndex        int    `json:"call_index"`
	Attempt          int    `json:"attempt"`
	ToolName         string `json:"tool_name"`
	TriggerErrorCode string `json:"trigger_error_code"`
	RejectedArgsHash string `json:"rejected_args_hash"`
	Outcome          string `json:"outcome"`
}

type ExecutionRequest struct {
	Message       string
	Plan          Plan
	CallIDPrefix  string
	TraceID       string
	Strategy      string
	Principal     toolruntime.Principal
	AllowedEffect toolruntime.SideEffect
}

type ExecutionResult struct {
	Status            string
	Plan              Plan
	ToolMessages      []toolruntime.ToolMessage
	AttemptMessages   []toolruntime.ToolMessage
	Repairs           []RepairRecord
	RepairCount       int
	TerminationReason string
	CachedCount       int
}

// ExecuteCandidatePlan is the only outer repair loop for ToolAgent candidates.
// The governed runtime still owns validation and execution. Only schema errors
// are repairable, exact tool names are immutable, and no-progress ends the run.
func ExecuteCandidatePlan(ctx context.Context, runtime RuntimeInvoker, planner CandidatePlanner, request ExecutionRequest) ExecutionResult {
	result := ExecutionResult{
		Status:          request.Plan.Decision,
		Plan:            request.Plan,
		ToolMessages:    []toolruntime.ToolMessage{},
		AttemptMessages: []toolruntime.ToolMessage{},
		Repairs:         []RepairRecord{},
	}
	if request.Plan.Decision != "execute" || runtime == nil || planner == nil {
		return result
	}
	if len(result.Plan.Calls) == 0 {
		result.Status = "failed"
		result.TerminationReason = "EMPTY_EXECUTION_PLAN"
		return result
	}
	if len(result.Plan.Calls) > MaxPlanCalls {
		result.Plan.OmittedCount += len(result.Plan.Calls) - MaxPlanCalls
		result.Plan.Calls = result.Plan.Calls[:MaxPlanCalls]
	}
	actionGuard := toolruntime.NewActionGuard()
	failed := 0
	for index, call := range result.Plan.Calls {
		current := call
		var message toolruntime.ToolMessage
		for repairAttempt := 0; ; repairAttempt++ {
			message = runtime.Invoke(ctx, toolruntime.Invocation{
				CallID:  request.CallIDPrefix + "-" + strconv.Itoa(index+1) + "-" + strconv.Itoa(repairAttempt+1),
				TraceID: request.TraceID, ToolName: current.ToolName, Arguments: current.Arguments,
				Intent: "tool_task", Strategy: request.Strategy, Principal: request.Principal,
				AllowedSideEffect: request.AllowedEffect,
				Budget:            toolruntime.CallBudget{MaxCalls: len(result.Plan.Calls), UsedCalls: index},
				ActionGuard:       actionGuard,
			})
			result.AttemptMessages = append(result.AttemptMessages, message)
			if message.ErrorCode != toolruntime.ErrorArgumentsInvalid {
				break
			}
			if repairAttempt >= MaxRepairAttempts {
				result.TerminationReason = "SCHEMA_REPAIR_LIMIT_REACHED"
				break
			}
			attempt := repairAttempt + 1
			record := RepairRecord{CallIndex: index, Attempt: attempt, ToolName: current.ToolName, TriggerErrorCode: message.ErrorCode, RejectedArgsHash: message.ArgsHash}
			repaired, err := planner.Repair(request.Message, current, RepairFeedback{
				CallIndex: index, Attempt: attempt, ToolName: current.ToolName,
				ErrorCode: message.ErrorCode, RejectedArgsHash: message.ArgsHash,
			})
			if err != nil {
				record.Outcome = "repair_unavailable"
				result.TerminationReason = "SCHEMA_REPAIR_UNAVAILABLE"
				if errors.Is(err, ErrRepairNoProgress) {
					record.Outcome = "no_progress"
					result.TerminationReason = "SCHEMA_REPAIR_NO_PROGRESS"
				}
				result.Repairs = append(result.Repairs, record)
				break
			}
			if repaired.ToolName != current.ToolName {
				record.Outcome = "rejected_tool_change"
				result.Repairs = append(result.Repairs, record)
				result.TerminationReason = "REPAIR_TOOL_CHANGE_REJECTED"
				break
			}
			record.Outcome = "accepted_candidate"
			result.Repairs = append(result.Repairs, record)
			result.RepairCount++
			current = repaired
			result.Plan.Calls[index] = repaired
		}
		result.ToolMessages = append(result.ToolMessages, message)
		if message.Cached {
			result.CachedCount++
		}
		if message.Status != toolruntime.StatusSuccess {
			failed++
		}
		if message.ErrorCode == toolruntime.ErrorToolNotRegistered {
			result.TerminationReason = "UNKNOWN_TOOL_REJECTED"
		}
		if message.ErrorCode == toolruntime.ErrorNoProgress {
			result.TerminationReason = "NO_PROGRESS"
		}
		if result.TerminationReason != "" {
			break
		}
	}
	result.Status = "succeeded"
	if failed == len(result.ToolMessages) {
		result.Status = "failed"
	} else if failed > 0 {
		result.Status = "partial_failed"
	}
	return result
}
