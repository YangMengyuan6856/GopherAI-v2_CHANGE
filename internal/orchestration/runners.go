package orchestration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/rag"
)

type KnowledgeAnswerer interface {
	Answer(context.Context, knowledgeagent.Input) (knowledgeagent.Output, error)
}

type CaseDiagnosticAnalyzer interface {
	Analyze(context.Context, string, string, string) (diagnostic.CaseStrategyResult, error)
}

type KnowledgeRunner struct{ answerer KnowledgeAnswerer }

func NewKnowledgeRunner(answerer KnowledgeAnswerer) (*KnowledgeRunner, error) {
	if answerer == nil {
		return nil, fmt.Errorf("knowledge answerer is required")
	}
	return &KnowledgeRunner{answerer: answerer}, nil
}

func (runner *KnowledgeRunner) Run(ctx context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
	if runner == nil || runner.answerer == nil || task.Agent != KnowledgeAgentRole {
		return AgentOutput{}, fmt.Errorf("knowledge runner boundary is invalid")
	}
	result, err := runner.answerer.Answer(ctx, knowledgeagent.Input{
		TenantID: input.TenantID, UserID: input.UserID, Question: input.Message, TopK: rag.DefaultTopK,
	})
	if err != nil {
		return AgentOutput{}, err
	}
	evidence := make([]SharedEvidence, 0, len(result.Result.Evidence))
	for _, item := range result.Result.Evidence {
		sourceType := strings.TrimSpace(item.Kind)
		if sourceType == "" {
			sourceType = "document_chunk"
		}
		evidence = append(evidence, SharedEvidence{
			ID: item.ID, SourceType: sourceType, Summary: boundedRunes(item.Content, maxEvidenceSummaryRunes),
			TenantID: item.TenantID, SourceID: item.SourceID, SourceVersion: item.SourceVersion,
			LineStart: item.LineStart, LineEnd: item.LineEnd, ContentHash: item.ContentHash,
			ParentEvidenceID: item.ParentEvidenceID, SourceKind: item.SourceKind, SourceRevision: item.SourceRevision,
			Authority: item.Authority, EffectiveAt: item.EffectiveAt, ExpiredAt: item.ExpiredAt,
			SupersedesVersion: item.SupersedesVersion, Score: item.Score,
		})
	}
	claims := []AgentClaim{}
	if len(result.Result.Citations) > 0 && strings.TrimSpace(result.Result.Answer) != "" {
		references := make([]string, 0, len(result.Result.Citations))
		for _, citation := range result.Result.Citations {
			references = appendUniqueStable(references, strings.TrimSpace(citation.EvidenceID))
		}
		claims = append(claims, AgentClaim{
			ID: "knowledge-grounded-answer", Kind: "project_knowledge",
			Statement:    boundedRunes(result.Result.Answer, maxClaimStatementRunes),
			EvidenceRefs: references, Confidence: result.Result.Confidence,
		})
	}
	outcome := AgentOutcomeCompleted
	if len(claims) == 0 {
		outcome = AgentOutcomeInsufficient
	}
	return AgentOutput{
		Outcome: outcome,
		Summary: boundedRunes(result.Result.Answer, maxAgentSummaryRunes), Claims: claims, Evidence: evidence,
		FollowUps: append([]string{}, result.Result.FollowUpQuestions...), Usage: result.Result.Usage,
		ToolCalls: 1, Iterations: maximum(1, result.Answer.ModelAttempts), OutputReason: result.Answer.ReasonCode,
	}, nil
}

type DiagnosticRunner struct{ analyzer CaseDiagnosticAnalyzer }

func NewDiagnosticRunner(analyzer CaseDiagnosticAnalyzer) (*DiagnosticRunner, error) {
	if analyzer == nil {
		return nil, fmt.Errorf("diagnostic analyzer is required")
	}
	return &DiagnosticRunner{analyzer: analyzer}, nil
}

func (runner *DiagnosticRunner) Run(ctx context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
	if runner == nil || runner.analyzer == nil || task.Agent != DiagnosticAgentRole {
		return AgentOutput{}, fmt.Errorf("diagnostic runner boundary is invalid")
	}
	result, err := runner.analyzer.Analyze(ctx, input.TenantID, input.UserID, input.Message)
	if err != nil {
		return AgentOutput{}, err
	}
	baseline := result.Baseline
	evidenceByID := make(map[string]SharedEvidence)
	claims := make([]AgentClaim, 0, len(baseline.Hypotheses))
	for _, hypothesis := range baseline.Hypotheses {
		references := make([]string, 0, len(hypothesis.Evidence))
		for _, item := range hypothesis.Evidence {
			references = appendUniqueStable(references, item.ID)
			if current, exists := evidenceByID[item.ID]; !exists || hypothesis.Confidence > current.Score {
				evidenceByID[item.ID] = SharedEvidence{
					ID: item.ID, SourceType: item.SourceType, Summary: item.Summary,
					TenantID: input.TenantID, SourceID: item.ID, SourceVersion: diagnostic.SchemaVersion,
					Score: hypothesis.Confidence,
				}
			}
		}
		statement := hypothesis.Cause + "。" + hypothesis.Rationale
		claims = append(claims, AgentClaim{
			ID: hypothesis.ID, Kind: "diagnostic_hypothesis", Statement: boundedRunes(statement, maxClaimStatementRunes),
			EvidenceRefs: references, Confidence: hypothesis.Confidence,
		})
	}
	evidenceIDs := make([]string, 0, len(evidenceByID))
	for evidenceID := range evidenceByID {
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	sort.Strings(evidenceIDs)
	evidence := make([]SharedEvidence, 0, len(evidenceIDs))
	for _, evidenceID := range evidenceIDs {
		evidence = append(evidence, evidenceByID[evidenceID])
	}
	followUps := make([]string, 0, len(baseline.MissingInformation))
	for _, missing := range baseline.MissingInformation {
		followUps = append(followUps, missing.Question)
	}
	summary := fmt.Sprintf("标准诊断形成 %d 个待验证假设；案例记忆=%s/%s，仅作优先级建议。", len(claims), result.CaseMemoryStatus, result.CaseStrength)
	if result.PriorityRecommendation != nil {
		summary += fmt.Sprintf(" 历史确认案例建议优先核对假设 %s（相似度 %.0f%%），不视为当前根因。", result.PriorityRecommendation.HypothesisID, result.PriorityRecommendation.Similarity*100)
	}
	outcome := AgentOutcomeCompleted
	if len(claims) == 0 {
		outcome = AgentOutcomeInsufficient
	}
	return AgentOutput{
		Outcome: outcome,
		Summary: boundedRunes(summary, maxAgentSummaryRunes), Claims: claims, Evidence: evidence, FollowUps: followUps,
		ToolCalls: 1, Iterations: 1, OutputReason: result.ReasonCode,
	}, nil
}

func boundedRunes(value string, maximum int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}

func maximum(left, right int) int {
	if left > right {
		return left
	}
	return right
}
