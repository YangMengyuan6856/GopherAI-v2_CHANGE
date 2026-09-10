package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/rag"
	"github.com/cloudwego/eino/schema"
)

// The original runners remain unchanged for frozen rule-workflow comparisons.
// These adapters actually consume model-authored objectives and explicit handoffs.
type DelegatedKnowledgeRunner struct{ answerer KnowledgeAnswerer }

func NewDelegatedKnowledgeRunner(answerer KnowledgeAnswerer) *DelegatedKnowledgeRunner {
	return &DelegatedKnowledgeRunner{answerer: answerer}
}

func (r *DelegatedKnowledgeRunner) Run(ctx context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
	if r.answerer == nil || task.Agent != KnowledgeAgentRole {
		return AgentOutput{}, errors.New("knowledge delegate unavailable")
	}
	// Shared evidence is a query hint, never pasted into the corpus as new truth.
	// The independently retrieved answer still has to pass the existing RAG gate.
	question := task.Objective
	if len(input.SharedEvidence) > 0 {
		question += "\n已有证据中的来源线索（请重新检索核对，不把线索当新证据）："
		for _, e := range input.SharedEvidence {
			question += "\n" + boundedRunes(e.SourceID+" "+e.Summary, 250)
		}
	}
	result, err := r.answerer.Answer(ctx, knowledgeagent.Input{TenantID: input.TenantID, UserID: input.UserID, Question: question, TopK: rag.DefaultTopK})
	if err != nil {
		return AgentOutput{}, err
	}
	out := emptyAgentOutput()
	out.Summary = boundedRunes(result.Result.Answer, maxAgentSummaryRunes)
	out.Usage = result.Result.Usage
	out.Iterations = maximum(1, result.Answer.ModelAttempts)
	out.ToolCalls = 1
	out.OutputReason = result.Answer.ReasonCode
	out.FollowUps = append([]string{}, result.Result.FollowUpQuestions...)
	if len(out.FollowUps) > maxAgentFollowUps {
		out.FollowUps = out.FollowUps[:maxAgentFollowUps]
	}
	// A repair/fallback excerpt may have valid citations but is not a resolved answer.
	if !result.Result.Resolved {
		return out, nil
	}
	refs := []string{}
	cited := map[string]bool{}
	for _, c := range result.Result.Citations {
		if !cited[c.EvidenceID] && len(refs) < 10 {
			refs = append(refs, c.EvidenceID)
			cited[c.EvidenceID] = true
		}
	}
	for _, e := range result.Result.Evidence {
		if cited[e.ID] {
			out.Evidence = append(out.Evidence, SharedEvidence{ID: e.ID, Title: e.Title, SourceType: "document_chunk", Summary: boundedRunes(e.Content, maxEvidenceSummaryRunes), TenantID: e.TenantID, SourceID: e.SourceID, SourceVersion: e.SourceVersion, LineStart: e.LineStart, LineEnd: e.LineEnd, ContentHash: e.ContentHash, ParentEvidenceID: e.ParentEvidenceID, SourceKind: e.SourceKind, SourceRevision: e.SourceRevision, Authority: e.Authority, Score: e.Score})
		}
	}
	if len(refs) > 0 {
		out.Outcome = AgentOutcomeCompleted
		out.Claims = []AgentClaim{{ID: "knowledge-answer", Kind: "project_knowledge", Statement: boundedRunes(result.Result.Answer, maxClaimStatementRunes), EvidenceRefs: refs, Confidence: result.Result.Confidence}}
	}
	return out, nil
}

type DelegatedDiagnosticRunner struct {
	base  AgentRunner
	model CollaborationModel
}

func NewDelegatedDiagnosticRunner(base AgentRunner, m CollaborationModel) *DelegatedDiagnosticRunner {
	return &DelegatedDiagnosticRunner{base: base, model: m}
}

func (r *DelegatedDiagnosticRunner) Run(ctx context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
	if r.base == nil || r.model == nil || task.Agent != DiagnosticAgentRole {
		return AgentOutput{}, errors.New("diagnostic delegate unavailable")
	}
	// Only the actual original user report reaches the rule extractor. Neither
	// the supervisor's objective nor a document assertion becomes an observed error.
	base, err := r.base.Run(ctx, task, ExecutionInput{TenantID: input.TenantID, UserID: input.UserID, Message: input.Message})
	if err != nil {
		return AgentOutput{}, err
	}
	allowed := map[string]SharedEvidence{}
	for _, e := range append(append([]SharedEvidence{}, base.Evidence...), input.SharedEvidence...) {
		if e.TenantID != input.TenantID {
			return AgentOutput{}, errors.New("cross-tenant handoff rejected")
		}
		if prior, ok := allowed[e.ID]; ok && !sameEvidenceIdentity(prior, e) {
			return AgentOutput{}, errors.New("evidence identity collision")
		}
		allowed[e.ID] = e
	}
	if len(allowed) == 0 {
		base.Outcome = AgentOutcomeInsufficient
		base.OutputReason = "no_diagnostic_evidence"
		return base, nil
	}
	payload, _ := json.Marshal(map[string]any{"original_request": input.Message, "objective": task.Objective, "rule_result": base, "shared_evidence": input.SharedEvidence})
	if len(payload) > dynamicContextBytes {
		return AgentOutput{}, errors.New("diagnostic context budget exceeded")
	}
	response, err := r.model.Generate(ctx, []*schema.Message{schema.SystemMessage(delegatedDiagnosticPrompt), schema.UserMessage(string(payload))})
	out := emptyAgentOutput()
	out.Usage = addModelUsage(base.Usage, messageUsage(response))
	out.ToolCalls = base.ToolCalls
	out.Iterations = base.Iterations + 1
	if err != nil {
		out.OutputReason = "diagnostic_model_failed"
		return out, err
	}
	var answer struct {
		Summary string `json:"summary"`
		Claims  []struct {
			Statement    string   `json:"statement"`
			EvidenceRefs []string `json:"evidence_refs"`
		} `json:"claims"`
		FollowUps []string `json:"follow_ups"`
	}
	if response == nil || len(response.Content) > 14000 || strictJSON(response.Content, &answer) != nil || strings.TrimSpace(answer.Summary) == "" || len(answer.Claims) > 3 || len(answer.FollowUps) > 5 {
		out.OutputReason = "invalid_diagnostic_output"
		return out, errors.New(out.OutputReason)
	}
	out.Summary = cleanDynamicText(answer.Summary, maxAgentSummaryRunes)
	out.FollowUps = []string{}
	for _, q := range answer.FollowUps {
		out.FollowUps = append(out.FollowUps, cleanDynamicText(q, 500))
	}
	cited := map[string]bool{}
	for i, c := range answer.Claims {
		if len(c.EvidenceRefs) == 0 || len(c.EvidenceRefs) > 8 || strings.TrimSpace(c.Statement) == "" {
			return usageOnlyOutput(out), errors.New("diagnostic claim requires citations")
		}
		for _, id := range c.EvidenceRefs {
			if _, ok := allowed[id]; !ok {
				return usageOnlyOutput(out), errors.New("unknown diagnostic evidence")
			}
			cited[id] = true
		}
		out.Claims = append(out.Claims, AgentClaim{ID: fmt.Sprintf("diagnostic-%d", i+1), Kind: "diagnostic_hypothesis", Statement: cleanDynamicText(c.Statement, maxClaimStatementRunes), EvidenceRefs: c.EvidenceRefs, Confidence: 0})
	}
	for _, e := range sortedEvidence(allowed) {
		if cited[e.ID] {
			out.Evidence = append(out.Evidence, e)
		}
	}
	if len(out.Claims) > 0 {
		out.Outcome = AgentOutcomeCompleted
		out.OutputReason = "evidence_bound_diagnostic_hypotheses"
	} else {
		out.OutputReason = "diagnostic_needs_more_evidence"
	}
	return out, nil
}
