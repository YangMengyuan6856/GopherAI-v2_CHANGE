package orchestration

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"GopherAI/internal/policy"
)

func TestBoundedPlannerKeepsSimpleFailureOnSingleAgent(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), "Redis 返回 NOAUTH Authentication required")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionSingleAgent || plan.Strategy != policy.DiagnosisStandardStrategyName || plan.FallbackStrategy != "direct_fallback" {
		t.Fatalf("simple failure escaped single-agent baseline: %+v", plan)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].Agent != DiagnosticAgentRole || plan.Budget.MaxAgents != 1 || plan.Tasks[0].MaySpawnAgents {
		t.Fatalf("simple plan violated agent bound: %+v", plan)
	}
}

func TestBoundedPlannerSplitsIndependentFailuresIntoExactlyTwoTasks(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	message := "Redis 返回 NOAUTH；同时 RabbitMQ 返回 PRECONDITION_FAILED，请分别定位两条故障链。"
	plan, err := planner.Plan(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionCollaborative || plan.Strategy != policy.DiagnosisCollaborativeStrategyName || plan.ReasonCode != "independent_diagnostic_branches" {
		t.Fatalf("compound failure was not planned collaboratively: %+v", plan)
	}
	if len(plan.Tasks) != 2 || plan.Budget.MaxAgents != 2 || plan.Tasks[0].Agent != KnowledgeAgentRole || plan.Tasks[1].Agent != DiagnosticAgentRole {
		t.Fatalf("collaborative plan is not stable and bounded: %+v", plan)
	}
	for _, task := range plan.Tasks {
		if task.MaySpawnAgents {
			t.Fatalf("recursive agent spawning was enabled: %+v", task)
		}
	}
}

func TestBoundedPlannerSplitsKnowledgeVerificationFromDiagnosis(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), "生产后端返回 HTTP 502，同时根据项目部署手册核对 upstream 端口和发布配置。")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionCollaborative || plan.ReasonCode != "knowledge_diagnostic_split" || !plan.Signals.HasKnowledgeVerification {
		t.Fatalf("knowledge verification was not isolated from diagnosis: %+v", plan)
	}
}

func TestBoundedPlannerRecognizesExplicitProjectFileVerification(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), "生产发布后服务返回 HTTP 502；同时请根据 m3b-config.json 核对 release.probe_code 和 timeout_seconds，并给出只读故障排查假设。")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionCollaborative || plan.ReasonCode != "knowledge_diagnostic_split" || !plan.Signals.HasKnowledgeVerification || plan.ComplexityScore < plan.ComplexityThreshold {
		t.Fatalf("explicit project file did not open the collaboration gate: %+v", plan)
	}
}

func TestBoundedPlannerDoesNotStartMultipleAgentsForFileQuestionAlone(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), "请解释 m3b-config.json 的字段结构。")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionSingleAgent || len(plan.Tasks) != 1 {
		t.Fatalf("a simple file question incorrectly started multiple agents: %+v", plan)
	}
}

func TestBoundedPlannerDoesNotExposeSanitizedInputOrSecretsInTasks(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), "Redis NOAUTH，同时核对项目文档。password=super-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SanitizationRedactions < 1 {
		t.Fatalf("secret was not redacted before planning: %+v", plan)
	}
	for _, task := range plan.Tasks {
		if task.InputReference != "sanitized_diagnostic_input" || strings.Contains(task.Objective, "super-secret-value") {
			t.Fatalf("raw caller input leaked into public plan: %+v", task)
		}
	}
}

func TestBoundedPlannerIsDeterministic(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	message := "Redis NOAUTH，同时 RabbitMQ PRECONDITION_FAILED，请核对项目文档。"
	first, err := planner.Plan(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.Plan(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same request produced different plan:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestBoundedPlannerPropagatesCallerCancellation(t *testing.T) {
	planner := NewDefaultBoundedPlanner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := planner.Plan(ctx, "Redis NOAUTH"); err != context.Canceled {
		t.Fatalf("caller cancellation was not propagated: %v", err)
	}
}
