package intent

import (
	"GopherAI/internal/contract"
	"context"
	"errors"
	"time"
)

const CascadeVersion = "intent-cascade-v1"

type PatternStage interface {
	Recognize(input PatternInput) PatternDecision
}

type PrototypeStage interface {
	Recognize(ctx context.Context, question string) (PrototypeDecision, error)
}

type LLMStage interface {
	Recognize(ctx context.Context, input LLMInput) LLMDecision
}

type CascadeInput struct {
	Question          string
	KnowledgeRequired bool
	PreviousIntent    string
}

type CascadeDiagnostics struct {
	Version           string              `json:"version"`
	FinalStage        string              `json:"final_stage"`
	PrototypeCalled   bool                `json:"prototype_called"`
	LLMCalled         bool                `json:"llm_called"`
	PrototypeScores   []PrototypeScore    `json:"prototype_scores,omitempty"`
	PrototypeMargin   float64             `json:"prototype_margin,omitempty"`
	FallbackReasons   []string            `json:"fallback_reasons,omitempty"`
	LLMUsage          contract.ModelUsage `json:"llm_usage"`
	LatencyMillis     int64               `json:"latency_ms"`
	PatternReasons    []string            `json:"pattern_reasons"`
	PatternCandidates []string            `json:"pattern_candidates,omitempty"`
}

type CascadeDecision struct {
	Result      contract.IntentResult `json:"result"`
	Diagnostics CascadeDiagnostics    `json:"diagnostics"`
}

type CascadeRecognizer struct {
	pattern   PatternStage
	prototype PrototypeStage
	llm       LLMStage
}

func NewCascadeRecognizer(pattern PatternStage, prototype PrototypeStage, llm LLMStage) (*CascadeRecognizer, error) {
	if pattern == nil || prototype == nil || llm == nil {
		return nil, errors.New("pattern, prototype and llm intent stages are required")
	}
	return &CascadeRecognizer{pattern: pattern, prototype: prototype, llm: llm}, nil
}

func (recognizer *CascadeRecognizer) Recognize(ctx context.Context, input CascadeInput) (decision CascadeDecision) {
	startedAt := time.Now()
	defer func() { decision.Diagnostics.LatencyMillis = time.Since(startedAt).Milliseconds() }()
	decision.Diagnostics.Version = CascadeVersion

	pattern := recognizer.pattern.Recognize(PatternInput{
		Question: input.Question, KnowledgeRequired: input.KnowledgeRequired, PreviousIntent: input.PreviousIntent,
	})
	decision.Diagnostics.PatternReasons = append([]string(nil), pattern.ReasonCodes...)
	decision.Diagnostics.PatternCandidates = append([]string(nil), pattern.CandidateSet...)
	if pattern.Matched && !pattern.Result.IsCompound {
		decision.Result = cascadeResult(pattern.Result)
		decision.Diagnostics.FinalStage = "pattern"
		return decision
	}

	decision.Diagnostics.PrototypeCalled = true
	prototype, prototypeErr := recognizer.prototype.Recognize(ctx, input.Question)
	if prototypeErr != nil {
		decision.Diagnostics.FallbackReasons = append(decision.Diagnostics.FallbackReasons, "prototype_unavailable")
	} else {
		decision.Diagnostics.PrototypeScores = append([]PrototypeScore(nil), prototype.Scores...)
		decision.Diagnostics.PrototypeMargin = prototype.Margin
		if prototype.Result.Intent == FollowUp && !validPreviousIntent(input.PreviousIntent) {
			prototype.Matched = false
			decision.Diagnostics.FallbackReasons = append(decision.Diagnostics.FallbackReasons, "prototype_follow_up_context_missing")
		}
		if prototype.Matched && !pattern.Result.IsCompound {
			prototype.Result.Stages = append(copyStages(pattern.Result.Stages), prototype.Result.Stages...)
			decision.Result = cascadeResult(prototype.Result)
			decision.Diagnostics.FinalStage = "prototype"
			return decision
		}
	}

	decision.Diagnostics.LLMCalled = true
	llm := recognizer.llm.Recognize(ctx, LLMInput{
		Question: input.Question, PreviousIntent: input.PreviousIntent, Candidates: decision.Diagnostics.PrototypeScores,
	})
	decision.Diagnostics.LLMUsage = llm.Usage
	if llm.Status == LLMStatusCompleted {
		stages := copyStages(pattern.Result.Stages)
		if prototypeErr == nil {
			stages = append(stages, prototype.Result.Stages...)
		}
		llm.Result.Stages = append(stages, llm.Result.Stages...)
		decision.Result = cascadeResult(llm.Result)
		decision.Diagnostics.FinalStage = "llm"
		return decision
	}
	decision.Diagnostics.FallbackReasons = append(decision.Diagnostics.FallbackReasons, llm.OutcomeReason)
	decision.Result = degradedCascadeResult(pattern, prototype, prototypeErr)
	decision.Diagnostics.FinalStage = "degraded_clarification"
	return decision
}

func cascadeResult(result contract.IntentResult) contract.IntentResult {
	result.Version = CascadeVersion
	return result
}

func degradedCascadeResult(pattern PatternDecision, prototype PrototypeDecision, prototypeErr error) contract.IntentResult {
	result := pattern.Result
	if len(pattern.CandidateSet) == 0 || result.Intent == General {
		if prototypeErr == nil && IsKnown(prototype.Result.Intent) && prototype.Result.Intent != FollowUp {
			result = prototype.Result
		} else {
			result = contract.IntentResult{Intent: General}
		}
	}
	if result.Confidence > 0.59 {
		result.Confidence = 0.59
	}
	result.NeedsClarify = true
	result.Version = CascadeVersion
	result.Stages = append(copyStages(result.Stages), contract.IntentStageResult{
		Stage: "cascade", Intent: result.Intent, Confidence: result.Confidence, ReasonCode: "cascade_degraded_requires_clarification",
	})
	return result
}

func validPreviousIntent(value string) bool {
	return IsKnown(value) && value != FollowUp && value != General
}

func copyStages(stages []contract.IntentStageResult) []contract.IntentStageResult {
	result := make([]contract.IntentStageResult, len(stages))
	copy(result, stages)
	return result
}
