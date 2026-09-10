package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"GopherAI/internal/contract"
	"GopherAI/internal/toolruntime"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type dynamicModelFunc func(context.Context, []*schema.Message) (*schema.Message, error)

func (f dynamicModelFunc) Generate(c context.Context, m []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return f(c, m)
}
func modelJSON(v any) *schema.Message {
	b, _ := json.Marshal(v)
	return schema.AssistantMessage(string(b), nil)
}
func delegate(agent, objective string, refs ...string) SupervisorDecision {
	return SupervisorDecision{Action: "delegate", Update: "按当前证据核查下一项", Tasks: []Delegation{{Agent: agent, Objective: objective, EvidenceIDs: refs}}, Questions: []string{}}
}
func finish() SupervisorDecision {
	return SupervisorDecision{Action: "finish", Update: "完成已有证据分析，待现场确认", Tasks: []Delegation{}, Questions: []string{"请补充实际运行配置，不能仅依据文档确认根因。"}}
}
func defaultDynamicRunners() map[string]AgentRunner {
	return map[string]AgentRunner{
		KnowledgeAgentRole: runnerFunc(func(_ context.Context, t PlannedTask, in ExecutionInput) (AgentOutput, error) {
			return validAgentOutput(in.TenantID, t.TaskID), nil
		}),
		DiagnosticAgentRole: runnerFunc(func(_ context.Context, t PlannedTask, in ExecutionInput) (AgentOutput, error) {
			return validAgentOutput(in.TenantID, t.TaskID), nil
		}),
	}
}
func newDynamicTest(t *testing.T, m CollaborationModel, r map[string]AgentRunner) *DynamicSupervisor {
	t.Helper()
	s, e := NewDynamicSupervisor(m, "test-model", r, nil)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func dynamicInput() ExecutionInput {
	return ExecutionInput{TenantID: "alice", UserID: "alice", Message: "根据部署手册核对 Redis 配置，同时诊断 HTTP 502 和 Redis NOAUTH。password=private-value"}
}

func TestDynamicSupervisorReplansAndHandsRealEvidenceToDifferentObjective(t *testing.T) {
	calls := 0
	m := dynamicModelFunc(func(_ context.Context, msg []*schema.Message) (*schema.Message, error) {
		calls++
		if strings.Contains(msg[1].Content, "private-value") {
			t.Error("credential leaked into model")
		}
		switch calls {
		case 1:
			return modelJSON(delegate(KnowledgeAgentRole, "部署手册里 Redis 使用什么认证方式？")), nil
		case 2:
			if !strings.Contains(msg[1].Content, "d1:evidence-d1") {
				t.Error("evidence not fed back")
			}
			return modelJSON(delegate(DiagnosticAgentRole, "根据配置依据检查 NOAUTH 的待验证原因", "d1:evidence-d1")), nil
		default:
			return modelJSON(finish()), nil
		}
	})
	r := defaultDynamicRunners()
	r[DiagnosticAgentRole] = runnerFunc(func(_ context.Context, task PlannedTask, in ExecutionInput) (AgentOutput, error) {
		if task.Objective == in.Message || len(in.SharedEvidence) != 1 || in.SharedEvidence[0].ID != "d1:evidence-d1" {
			t.Error("delegation/context handoff was not applied")
		}
		out := validAgentOutput(in.TenantID, "diagnostic")
		out.Evidence = append(out.Evidence, in.SharedEvidence...)
		out.Claims[0].EvidenceRefs = append(out.Claims[0].EvidenceRefs, in.SharedEvidence[0].ID)
		return out, nil
	})
	s := newDynamicTest(t, m, r)
	out, e := s.Run(context.Background(), dynamicInput())
	if e != nil || out.Status != "complete" || out.Orchestration.SupervisorCalls != 3 || out.Orchestration.Delegations != 2 || len(out.Synthesis.Claims) != 2 || out.AffectsLiveTraffic {
		t.Fatalf("unexpected dynamic result: %+v %v", out, e)
	}
	if len(out.Synthesis.RejectedClaims) != 0 {
		t.Fatal(out.Synthesis.RejectedClaims)
	}
}

func TestDynamicSupervisorParallelBatchActuallyStartsBothChildren(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	calls := 0
	m := dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls++
		if calls == 1 {
			d := delegate(KnowledgeAgentRole, "核对配置")
			d.Tasks = append(d.Tasks, Delegation{Agent: DiagnosticAgentRole, Objective: "分析独立故障"})
			return modelJSON(d), nil
		}
		return modelJSON(finish()), nil
	})
	r := defaultDynamicRunners()
	for role := range r {
		r[role] = runnerFunc(func(ctx context.Context, task PlannedTask, in ExecutionInput) (AgentOutput, error) {
			started <- task.Agent
			select {
			case <-release:
			case <-ctx.Done():
				return AgentOutput{}, ctx.Err()
			}
			return validAgentOutput(in.TenantID, task.TaskID), nil
		})
	}
	s := newDynamicTest(t, m, r)
	done := make(chan CollaborationRun, 1)
	go func() { out, _ := s.Run(context.Background(), dynamicInput()); done <- out }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("children ran serially")
		}
	}
	close(release)
	out := <-done
	if len(out.Orchestration.Steps[0].Tasks) != 2 || out.Status != "complete" {
		t.Fatal(out.Status)
	}
}

func TestDynamicSupervisorRejectsUnknownRoleAndFabricatedHandoff(t *testing.T) {
	for _, decision := range []SupervisorDecision{delegate("ShellAgent", "删除文件"), delegate(KnowledgeAgentRole, "核对配置", "invented-evidence")} {
		calls := 0
		r := defaultDynamicRunners()
		for role := range r {
			r[role] = runnerFunc(func(context.Context, PlannedTask, ExecutionInput) (AgentOutput, error) {
				calls++
				return AgentOutput{}, nil
			})
		}
		s := newDynamicTest(t, dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) { return modelJSON(decision), nil }), r)
		out, _ := s.Run(context.Background(), dynamicInput())
		if calls != 0 || out.Orchestration.StopReason != "invalid_model_output" {
			t.Fatalf("invalid task executed: %+v", out.Orchestration)
		}
	}
}

func TestDynamicSupervisorStopsRepeatedActionsAndPreservesPartialResult(t *testing.T) {
	d := delegate(KnowledgeAgentRole, "核对配置")
	s := newDynamicTest(t, dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) { return modelJSON(d), nil }), defaultDynamicRunners())
	out, _ := s.Run(context.Background(), dynamicInput())
	if out.Orchestration.Delegations != 1 || out.Orchestration.StopReason != "repeated_delegation" || out.Status != "partial" || len(out.Synthesis.Claims) != 1 {
		t.Fatal(out)
	}
}

func TestDynamicSupervisorCapsDelegationsAndRetainsAllRounds(t *testing.T) {
	n := 0
	m := dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) {
		n++
		if n == 3 {
			return modelJSON(finish()), nil
		}
		d := delegate(KnowledgeAgentRole, "配置问题"+string(rune('0'+n)))
		d.Tasks = append(d.Tasks, Delegation{Agent: DiagnosticAgentRole, Objective: "诊断问题" + string(rune('0'+n))})
		return modelJSON(d), nil
	})
	out, _ := newDynamicTest(t, m, defaultDynamicRunners()).Run(context.Background(), dynamicInput())
	if out.Orchestration.Delegations != 4 || len(out.Synthesis.Claims) != 4 || out.Status != "complete" {
		t.Fatalf("lost earlier result or exceeded budget: %+v", out)
	}
}

func TestDynamicSupervisorFailureDoesNotEraseSuccessfulSibling(t *testing.T) {
	n := 0
	m := dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) {
		n++
		if n > 1 {
			return modelJSON(finish()), nil
		}
		d := delegate(KnowledgeAgentRole, "核对配置")
		d.Tasks = append(d.Tasks, Delegation{Agent: DiagnosticAgentRole, Objective: "分析错误"})
		return modelJSON(d), nil
	})
	r := defaultDynamicRunners()
	r[DiagnosticAgentRole] = runnerFunc(func(context.Context, PlannedTask, ExecutionInput) (AgentOutput, error) {
		return AgentOutput{Usage: contract.ModelUsage{InputTokens: 77}}, errors.New("model failed")
	})
	out, _ := newDynamicTest(t, m, r).Run(context.Background(), dynamicInput())
	if out.Status != "partial" || len(out.Synthesis.Claims) != 1 || out.Orchestration.Usage.InputTokens < 77 {
		t.Fatal(out)
	}
}

func TestDynamicSupervisorCrossTenantEvidenceCannotReachNextRound(t *testing.T) {
	n := 0
	m := dynamicModelFunc(func(_ context.Context, msg []*schema.Message) (*schema.Message, error) {
		n++
		if strings.Contains(msg[1].Content, "other-tenant-secret") {
			t.Error("cross-tenant data leaked")
		}
		if n == 1 {
			return modelJSON(delegate(KnowledgeAgentRole, "核对文档")), nil
		}
		return modelJSON(finish()), nil
	})
	r := defaultDynamicRunners()
	r[KnowledgeAgentRole] = runnerFunc(func(context.Context, PlannedTask, ExecutionInput) (AgentOutput, error) {
		return validAgentOutput("mallory", "other-tenant-secret"), nil
	})
	out, _ := newDynamicTest(t, m, r).Run(context.Background(), dynamicInput())
	if len(out.Synthesis.Evidence) != 0 || out.Status != "insufficient" {
		t.Fatal(out)
	}
}

func TestDynamicSupervisorCancellationPropagates(t *testing.T) {
	m := dynamicModelFunc(func(ctx context.Context, _ []*schema.Message) (*schema.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s := newDynamicTest(t, m, defaultDynamicRunners())
	s.timeout = 10 * time.Millisecond
	start := time.Now()
	out, _ := s.Run(context.Background(), dynamicInput())
	if time.Since(start) > time.Second || out.Orchestration.SupervisorCalls != 1 || out.Status != "failed" {
		t.Fatal(out)
	}
}

type rejectingDynamicAudit struct {
	mu    sync.Mutex
	calls int
}

func (a *rejectingDynamicAudit) Record(context.Context, toolruntime.Invocation, toolruntime.ToolMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return errors.New("storage down")
}
func TestDynamicSupervisorAuditFailurePreventsDispatch(t *testing.T) {
	s := newDynamicTest(t, dynamicModelFunc(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return modelJSON(delegate(KnowledgeAgentRole, "核对配置")), nil
	}), defaultDynamicRunners())
	s.auditor = &rejectingDynamicAudit{}
	out, _ := s.Run(context.Background(), dynamicInput())
	if out.Orchestration.Delegations != 0 || out.Orchestration.StopReason != "audit_unavailable" || out.Executed {
		t.Fatal(out)
	}
}

func TestDelegatedDiagnosticDoesNotTurnObjectiveIntoObservedError(t *testing.T) {
	base := runnerFunc(func(_ context.Context, _ PlannedTask, in ExecutionInput) (AgentOutput, error) {
		if strings.Contains(in.Message, "NOAUTH") {
			t.Error("invented objective became user evidence")
		}
		return validAgentOutput(in.TenantID, "actual"), nil
	})
	m := dynamicModelFunc(func(_ context.Context, msg []*schema.Message) (*schema.Message, error) {
		if !strings.Contains(msg[1].Content, "shared_evidence") {
			t.Error("handoff absent")
		}
		return schema.AssistantMessage(`{"summary":"仅为待验证假设","claims":[{"statement":"需要进一步确认","evidence_refs":["nonexistent"]}],"follow_ups":[]}`, nil), nil
	})
	r := NewDelegatedDiagnosticRunner(base, m)
	_, err := r.Run(context.Background(), PlannedTask{Agent: DiagnosticAgentRole, Objective: "假设 Redis NOAUTH"}, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "后端报错"})
	if err == nil {
		t.Fatal("fabricated citation accepted")
	}
}
