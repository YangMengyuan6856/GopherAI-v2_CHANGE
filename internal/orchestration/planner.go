package orchestration

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"

	"GopherAI/internal/contract"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/policy"
)

const (
	PlanSchemaVersion     = "collaboration-plan-shadow-v1"
	PlannerVersion        = "bounded-collaboration-planner-v1"
	DecisionSingleAgent   = "single_agent"
	DecisionCollaborative = "collaborative_candidate"
	KnowledgeAgentRole    = "KnowledgeAgent"
	DiagnosticAgentRole   = "DiagnosticAgent"
	complexityThreshold   = 70
	maximumPlannedAgents  = 2
)

var (
	knowledgeVerificationPattern = regexp.MustCompile(`(?i)文档|手册|配置说明|发布清单|项目资料|知识库|引用|证据|document|manual|manifest|runbook`)
	conflictPattern              = regexp.MustCompile(`(?i)冲突|不一致|互相矛盾|分别显示|一个.+另一个|conflict|inconsistent|contradict`)
	highImpactPattern            = regexp.MustCompile(`(?i)生产|全站|大面积|数据丢失|资金|安全事件|production|outage|data loss|security incident`)
	separateClausePattern        = regexp.MustCompile(`(?i)同时|另外|并且|以及|分别|一边.+一边|\b(?:then|also|while|and)\b`)
)

type ComplexitySignals struct {
	ComponentCount              int  `json:"component_count"`
	ErrorSignatureCount         int  `json:"error_signature_count"`
	HasKnowledgeVerification    bool `json:"has_knowledge_verification"`
	HasEvidenceConflict         bool `json:"has_evidence_conflict"`
	HasHighImpactMarker         bool `json:"has_high_impact_marker"`
	HasSeparateClause           bool `json:"has_separate_clause"`
	HasIndependentFailureScopes bool `json:"has_independent_failure_scopes"`
}

type TaskBudget struct {
	MaxIterations   int `json:"max_iterations"`
	MaxToolCalls    int `json:"max_tool_calls"`
	MaxInputTokens  int `json:"max_input_tokens"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

type PlannedTask struct {
	Index          int        `json:"index"`
	TaskID         string     `json:"task_id"`
	Agent          string     `json:"agent"`
	Objective      string     `json:"objective"`
	InputReference string     `json:"input_reference"`
	OutputContract string     `json:"output_contract"`
	Budget         TaskBudget `json:"budget"`
	MaySpawnAgents bool       `json:"may_spawn_agents"`
}

type PlanBudget struct {
	MaxAgents       int   `json:"max_agents"`
	MaxToolCalls    int   `json:"max_tool_calls"`
	MaxIterations   int   `json:"max_iterations"`
	MaxInputTokens  int   `json:"max_input_tokens"`
	MaxOutputTokens int   `json:"max_output_tokens"`
	MaxCostMicros   int64 `json:"max_cost_micros"`
	TotalTimeoutMS  int64 `json:"total_timeout_ms"`
}

type CollaborationPlan struct {
	SchemaVersion          string            `json:"schema_version"`
	PlannerVersion         string            `json:"planner_version"`
	Mode                   string            `json:"mode"`
	AffectsLiveTraffic     bool              `json:"affects_live_traffic"`
	Decision               string            `json:"decision"`
	Strategy               string            `json:"strategy"`
	FallbackStrategy       string            `json:"fallback_strategy"`
	ReasonCode             string            `json:"reason_code"`
	ComplexityScore        int               `json:"complexity_score"`
	ComplexityThreshold    int               `json:"complexity_threshold"`
	Signals                ComplexitySignals `json:"signals"`
	Tasks                  []PlannedTask     `json:"tasks"`
	Budget                 PlanBudget        `json:"budget"`
	Limitations            []string          `json:"limitations"`
	SanitizationRedactions int               `json:"sanitization_redactions"`
}

type BoundedPlanner struct {
	registry  *policy.StrategyRegistry
	extractor diagnostic.Extractor
}

func NewBoundedPlanner(registry *policy.StrategyRegistry) (*BoundedPlanner, error) {
	if registry == nil {
		return nil, errors.New("strategy registry is required")
	}
	if _, exists := registry.Get(policy.DiagnosisStandardStrategyName); !exists {
		return nil, errors.New("diagnosis_standard strategy is required")
	}
	if _, exists := registry.Get(policy.DiagnosisCollaborativeStrategyName); !exists {
		return nil, errors.New("diagnosis_collaborative strategy is required")
	}
	return &BoundedPlanner{registry: registry}, nil
}

func NewDefaultBoundedPlanner() *BoundedPlanner {
	planner, err := NewBoundedPlanner(policy.DefaultStrategyRegistry())
	if err != nil {
		panic(err)
	}
	return planner
}

func (planner *BoundedPlanner) Plan(ctx context.Context, message string) (CollaborationPlan, error) {
	if planner == nil || planner.registry == nil {
		return CollaborationPlan{}, errors.New("bounded planner is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return CollaborationPlan{}, err
	}
	extracted, err := planner.extractor.Extract(message)
	if err != nil {
		return CollaborationPlan{}, err
	}
	signals := classifyComplexity(extracted)
	score := complexityScore(signals)
	decision := DecisionSingleAgent
	strategyName := policy.DiagnosisStandardStrategyName
	reasonCode := "single_task_preferred"
	if score >= complexityThreshold && (signals.HasIndependentFailureScopes || signals.HasKnowledgeVerification || signals.HasEvidenceConflict) {
		decision = DecisionCollaborative
		strategyName = policy.DiagnosisCollaborativeStrategyName
		switch {
		case signals.HasEvidenceConflict && signals.HasKnowledgeVerification:
			reasonCode = "conflict_requires_evidence_verification"
		case signals.HasKnowledgeVerification:
			reasonCode = "knowledge_diagnostic_split"
		default:
			reasonCode = "independent_diagnostic_branches"
		}
	}
	metadata, exists := planner.registry.Get(strategyName)
	if !exists {
		return CollaborationPlan{}, errors.New("planned strategy is unavailable")
	}
	tasks := singleDiagnosticTask(metadata.MaximumBudget)
	if decision == DecisionCollaborative {
		tasks = collaborativeTasks(metadata.MaximumBudget)
	}
	plan := CollaborationPlan{
		SchemaVersion: PlanSchemaVersion, PlannerVersion: PlannerVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Decision: decision, Strategy: strategyName, FallbackStrategy: metadata.Fallback,
		ReasonCode: reasonCode, ComplexityScore: score, ComplexityThreshold: complexityThreshold, Signals: signals,
		Tasks: tasks, Budget: publicBudget(metadata.MaximumBudget), SanitizationRedactions: extracted.RedactionCount,
		Limitations: []string{
			"复杂度分数来自确定性启发式，不代表多 Agent 已获得线上质量收益。",
			"本计划只生成公开结构化子任务，不保存或展示隐藏思维链。",
			"任何子 Agent 都禁止继续创建 Agent；执行能力将在独立质量门后接入。",
		},
	}
	if err := plan.validate(); err != nil {
		return CollaborationPlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return CollaborationPlan{}, err
	}
	return plan, nil
}

func classifyComplexity(input diagnostic.ExtractedInput) ComplexitySignals {
	text := input.SanitizedExcerpt
	components := independentComponents(input.Components)
	signals := ComplexitySignals{
		ComponentCount: len(components), ErrorSignatureCount: len(input.ErrorSignatures),
		HasKnowledgeVerification: knowledgeVerificationPattern.MatchString(text),
		HasEvidenceConflict:      conflictPattern.MatchString(text),
		HasHighImpactMarker:      highImpactPattern.MatchString(text),
		HasSeparateClause:        separateClausePattern.MatchString(text),
	}
	signals.HasIndependentFailureScopes = signals.ComponentCount >= 2 && signals.ErrorSignatureCount >= 2
	return signals
}

func independentComponents(components []string) []string {
	seen := make(map[string]struct{}, len(components))
	result := make([]string, 0, len(components))
	for _, component := range components {
		component = strings.TrimSpace(component)
		if component == "" || component == "docker" {
			continue
		}
		if _, exists := seen[component]; exists {
			continue
		}
		seen[component] = struct{}{}
		result = append(result, component)
	}
	sort.Strings(result)
	return result
}

func complexityScore(signals ComplexitySignals) int {
	score := 0
	if signals.ComponentCount > 0 || signals.ErrorSignatureCount > 0 {
		score += 20
	}
	if signals.HasIndependentFailureScopes {
		score += 30
	}
	if signals.ErrorSignatureCount >= 2 {
		score += 20
	}
	if signals.HasKnowledgeVerification {
		score += 40
	}
	if signals.HasEvidenceConflict {
		score += 20
	}
	if signals.HasHighImpactMarker {
		score += 10
	}
	if signals.HasSeparateClause {
		score += 10
	}
	if score > 100 {
		return 100
	}
	return score
}

func singleDiagnosticTask(budget contract.ExecutionBudgets) []PlannedTask {
	return []PlannedTask{{
		Index: 1, TaskID: "diagnostic-analysis", Agent: DiagnosticAgentRole,
		Objective:      "基于已脱敏故障输入形成有证据的排序假设与只读验证步骤。",
		InputReference: "sanitized_diagnostic_input", OutputContract: "diagnostic-result-v1",
		Budget: taskBudget(budget, 1), MaySpawnAgents: false,
	}}
}

func collaborativeTasks(budget contract.ExecutionBudgets) []PlannedTask {
	return []PlannedTask{
		{
			Index: 1, TaskID: "knowledge-verification", Agent: KnowledgeAgentRole,
			Objective:      "仅从当前用户有权访问的项目证据核对配置、版本、运行边界与冲突。",
			InputReference: "sanitized_diagnostic_input", OutputContract: "grounded-knowledge-evidence-v1",
			Budget: taskBudget(budget, maximumPlannedAgents), MaySpawnAgents: false,
		},
		{
			Index: 2, TaskID: "diagnostic-analysis", Agent: DiagnosticAgentRole,
			Objective:      "独立形成排序假设、当前证据与只读验证步骤，不把历史案例当成当前根因。",
			InputReference: "sanitized_diagnostic_input", OutputContract: "diagnostic-result-v1",
			Budget: taskBudget(budget, maximumPlannedAgents), MaySpawnAgents: false,
		},
	}
}

func taskBudget(total contract.ExecutionBudgets, divisor int) TaskBudget {
	if divisor < 1 {
		divisor = 1
	}
	return TaskBudget{
		MaxIterations: total.MaxIterations / divisor, MaxToolCalls: total.MaxToolCalls / divisor,
		MaxInputTokens: total.MaxInputTokens / divisor, MaxOutputTokens: total.MaxOutputTokens / divisor,
	}
}

func publicBudget(budget contract.ExecutionBudgets) PlanBudget {
	return PlanBudget{
		MaxAgents: budget.MaxAgents, MaxToolCalls: budget.MaxToolCalls, MaxIterations: budget.MaxIterations,
		MaxInputTokens: budget.MaxInputTokens, MaxOutputTokens: budget.MaxOutputTokens,
		MaxCostMicros: budget.MaxCostMicros, TotalTimeoutMS: budget.TotalTimeout.Milliseconds(),
	}
}

func (plan CollaborationPlan) validate() error {
	if plan.SchemaVersion != PlanSchemaVersion || plan.PlannerVersion != PlannerVersion || plan.Mode != "shadow_only" || plan.AffectsLiveTraffic {
		return errors.New("plan boundary is invalid")
	}
	if len(plan.Tasks) < 1 || len(plan.Tasks) > maximumPlannedAgents || plan.Budget.MaxAgents < len(plan.Tasks) || plan.Budget.MaxAgents > maximumPlannedAgents {
		return errors.New("plan agent budget is invalid")
	}
	seen := make(map[string]struct{}, len(plan.Tasks))
	totalIterations, totalToolCalls, totalInputTokens, totalOutputTokens := 0, 0, 0, 0
	for index, task := range plan.Tasks {
		if task.Index != index+1 || strings.TrimSpace(task.TaskID) == "" || task.MaySpawnAgents {
			return errors.New("planned task boundary is invalid")
		}
		if task.Agent != KnowledgeAgentRole && task.Agent != DiagnosticAgentRole {
			return errors.New("planned task uses an unknown agent")
		}
		if _, exists := seen[task.TaskID]; exists {
			return errors.New("planned task id is duplicated")
		}
		if task.Budget.MaxIterations < 1 || task.Budget.MaxToolCalls < 1 || task.Budget.MaxInputTokens < 1 || task.Budget.MaxOutputTokens < 1 {
			return errors.New("planned task budget is invalid")
		}
		totalIterations += task.Budget.MaxIterations
		totalToolCalls += task.Budget.MaxToolCalls
		totalInputTokens += task.Budget.MaxInputTokens
		totalOutputTokens += task.Budget.MaxOutputTokens
		seen[task.TaskID] = struct{}{}
	}
	if totalIterations > plan.Budget.MaxIterations || totalToolCalls > plan.Budget.MaxToolCalls || totalInputTokens > plan.Budget.MaxInputTokens || totalOutputTokens > plan.Budget.MaxOutputTokens {
		return errors.New("planned task budgets exceed the total budget")
	}
	return nil
}
