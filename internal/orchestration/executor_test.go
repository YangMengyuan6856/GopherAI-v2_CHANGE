package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/contract"
)

type runnerFunc func(context.Context, PlannedTask, ExecutionInput) (AgentOutput, error)

func (function runnerFunc) Run(ctx context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
	return function(ctx, task, input)
}

func collaborativeTestPlan(t *testing.T) CollaborationPlan {
	t.Helper()
	plan, err := NewDefaultBoundedPlanner().Plan(context.Background(), "Redis NOAUTH，同时 RabbitMQ PRECONDITION_FAILED，请核对项目文档。")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != DecisionCollaborative {
		t.Fatalf("test fixture did not create collaborative plan: %+v", plan)
	}
	return plan
}

func validAgentOutput(tenantID string, id string) AgentOutput {
	evidenceID := "evidence-" + id
	return AgentOutput{
		Summary:   "bounded result " + id,
		Claims:    []AgentClaim{{ID: "claim-" + id, Kind: "verification", Statement: "bounded claim " + id, EvidenceRefs: []string{evidenceID}, Confidence: .8}},
		Evidence:  []SharedEvidence{{ID: evidenceID, SourceType: "project_document", Summary: "bounded evidence " + id, TenantID: tenantID, Score: .9}},
		FollowUps: []string{}, Usage: contract.ModelUsage{InputTokens: 10, OutputTokens: 5, CostMicros: 10}, Iterations: 1,
	}
}

func TestParallelExecutorKeepsPlanOrderWhenAgentsFinishOutOfOrder(t *testing.T) {
	plan := collaborativeTestPlan(t)
	knowledgeRelease, diagnosticRelease := make(chan struct{}), make(chan struct{})
	started := make(chan string, 2)
	runners := map[string]AgentRunner{
		KnowledgeAgentRole: runnerFunc(func(_ context.Context, _ PlannedTask, input ExecutionInput) (AgentOutput, error) {
			started <- KnowledgeAgentRole
			<-knowledgeRelease
			return validAgentOutput(input.TenantID, "knowledge"), nil
		}),
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, _ PlannedTask, input ExecutionInput) (AgentOutput, error) {
			started <- DiagnosticAgentRole
			<-diagnosticRelease
			return validAgentOutput(input.TenantID, "diagnostic"), nil
		}),
	}
	executor, err := NewParallelExecutor(runners)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan ExecutionResult, 1)
	go func() {
		result, _ := executor.Execute(context.Background(), plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "safe"})
		done <- result
	}()
	<-started
	<-started
	close(diagnosticRelease)
	time.Sleep(5 * time.Millisecond)
	close(knowledgeRelease)
	result := <-done
	if result.Status != ExecutionCompleted || len(result.TaskResults) != 2 || result.TaskResults[0].Agent != KnowledgeAgentRole || result.TaskResults[1].Agent != DiagnosticAgentRole {
		t.Fatalf("parallel completion changed stable result order: %+v", result)
	}
}

func TestParallelExecutorBoundsIndividualTaskTimeoutWithoutCancellingSibling(t *testing.T) {
	plan := collaborativeTestPlan(t)
	plan.Tasks[0].Budget.TimeoutMS = 10
	executor, _ := NewParallelExecutor(map[string]AgentRunner{
		KnowledgeAgentRole: runnerFunc(func(ctx context.Context, _ PlannedTask, _ ExecutionInput) (AgentOutput, error) {
			<-ctx.Done()
			return AgentOutput{}, ctx.Err()
		}),
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, _ PlannedTask, input ExecutionInput) (AgentOutput, error) {
			return validAgentOutput(input.TenantID, "diagnostic"), nil
		}),
	})
	result, err := executor.Execute(context.Background(), plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "safe"})
	if err != nil || result.Status != ExecutionPartial || result.TaskResults[0].Status != TaskStatusTimedOut || result.TaskResults[1].Status != TaskStatusSucceeded {
		t.Fatalf("task timeout incorrectly cancelled sibling: result=%+v err=%v", result, err)
	}
}

func TestParallelExecutorPropagatesCallerCancellationToBothAgents(t *testing.T) {
	plan := collaborativeTestPlan(t)
	started := make(chan struct{}, 2)
	waitForCancel := runnerFunc(func(ctx context.Context, _ PlannedTask, _ ExecutionInput) (AgentOutput, error) {
		started <- struct{}{}
		<-ctx.Done()
		return AgentOutput{}, ctx.Err()
	})
	executor, _ := NewParallelExecutor(map[string]AgentRunner{KnowledgeAgentRole: waitForCancel, DiagnosticAgentRole: waitForCancel})
	ctx, cancel := context.WithCancel(context.Background())
	type response struct {
		result ExecutionResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := executor.Execute(ctx, plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "safe"})
		done <- response{result: result, err: err}
	}()
	<-started
	<-started
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || got.result.Status != ExecutionCancelled || len(got.result.TaskResults) != 2 {
		t.Fatalf("caller cancellation was masked: result=%+v err=%v", got.result, got.err)
	}
	for _, task := range got.result.TaskResults {
		if task.Status != TaskStatusCancelled {
			t.Fatalf("cancelled execution returned non-cancelled task: %+v", task)
		}
	}
}

func TestParallelExecutorDropsClaimsWhenTaskExceedsBudget(t *testing.T) {
	plan, err := NewDefaultBoundedPlanner().Plan(context.Background(), "Redis NOAUTH")
	if err != nil {
		t.Fatal(err)
	}
	executor, _ := NewParallelExecutor(map[string]AgentRunner{
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, task PlannedTask, input ExecutionInput) (AgentOutput, error) {
			output := validAgentOutput(input.TenantID, "diagnostic")
			output.Iterations = task.Budget.MaxIterations + 1
			return output, nil
		}),
	})
	result, err := executor.Execute(context.Background(), plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "safe"})
	if err != nil || result.Status != ExecutionBudget || result.TaskResults[0].Status != TaskStatusBudget || len(result.TaskResults[0].Output.Claims) != 0 || result.Usage.Iterations <= plan.Budget.MaxIterations {
		t.Fatalf("over-budget claims were not contained: result=%+v err=%v", result, err)
	}
}

func TestParallelExecutorRejectsCrossTenantEvidence(t *testing.T) {
	plan, _ := NewDefaultBoundedPlanner().Plan(context.Background(), "Redis NOAUTH")
	executor, _ := NewParallelExecutor(map[string]AgentRunner{
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, _ PlannedTask, _ ExecutionInput) (AgentOutput, error) {
			return validAgentOutput("mallory", "diagnostic"), nil
		}),
	})
	result, err := executor.Execute(context.Background(), plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "safe"})
	if err != nil || result.TaskResults[0].Status != TaskStatusFailed || result.TaskResults[0].ReasonCode != "agent_output_invalid" || len(result.TaskResults[0].Output.Evidence) != 0 {
		t.Fatalf("cross-tenant evidence escaped executor: result=%+v err=%v", result, err)
	}
}

func TestParallelExecutorRedactsInputBeforeAnyRunnerReceivesIt(t *testing.T) {
	plan, _ := NewDefaultBoundedPlanner().Plan(context.Background(), "Redis NOAUTH")
	received := make(chan string, 1)
	executor, _ := NewParallelExecutor(map[string]AgentRunner{
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, _ PlannedTask, input ExecutionInput) (AgentOutput, error) {
			received <- input.Message
			return validAgentOutput(input.TenantID, "diagnostic"), nil
		}),
	})
	result, err := executor.Execute(context.Background(), plan, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "Redis NOAUTH\npassword=super-secret-value"})
	message := <-received
	if err != nil || result.InputRedactions < 1 || strings.Contains(message, "super-secret-value") {
		t.Fatalf("raw secret reached runner: redactions=%d message=%q err=%v", result.InputRedactions, message, err)
	}
}
