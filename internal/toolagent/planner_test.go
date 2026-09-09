package toolagent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPlannerSelectsBoundedRealTools(t *testing.T) {
	cases := []struct {
		name      string
		message   string
		decision  string
		tools     []string
		healthArg string
		omitted   int
	}{
		{name: "manifest", message: "当前发布版本、Git SHA 和回滚策略是什么？", decision: "execute", tools: []string{"deployment_manifest_lookup"}},
		{name: "backend", message: "检查后端 Redis 和 MySQL 是否正常", decision: "execute", tools: []string{"service_health_snapshot"}, healthArg: `"service":"backend"`},
		{name: "worker", message: "索引 Worker ready 状态", decision: "execute", tools: []string{"service_health_snapshot"}, healthArg: `"service":"index_worker"`},
		{name: "compound", message: "给出当前发布清单，并检查后端和 Worker 健康状态", decision: "execute", tools: []string{"deployment_manifest_lookup", "service_health_snapshot"}, healthArg: `"service":"all"`},
		{name: "no tool", message: "解释 Go interface 的用途", decision: "answer_without_tool", tools: []string{}},
		{name: "unsafe", message: "重启后端并删除旧数据库记录", decision: "refuse", tools: []string{}},
		{name: "backend auth logs", message: "查看后端 NOAUTH 报错日志", decision: "execute", tools: []string{"service_health_snapshot", "bounded_log_signature"}, healthArg: `"service":"backend"`},
		{name: "worker warning logs", message: "检索 Worker slow sql 日志", decision: "execute", tools: []string{"service_health_snapshot", "bounded_log_signature"}, healthArg: `"service":"index_worker"`},
		{name: "MCP release", message: "请通过 MCP 协议源查询当前发布清单", decision: "execute", tools: []string{"mcp_deployment_evidence"}},
		{name: "official Redis evidence", message: "Redis NOAUTH 报错，请查询官方文档并检查依赖状态", decision: "execute", tools: []string{"official_document_search", "service_health_snapshot"}, healthArg: `"service":"backend"`, omitted: 1},
	}
	planner := NewPlanner()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := planner.Plan(testCase.message)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Decision != testCase.decision || len(plan.Calls) != len(testCase.tools) || len(plan.Calls) > MaxPlanCalls {
				t.Fatalf("unexpected plan: %+v", plan)
			}
			if plan.OmittedCount != testCase.omitted {
				t.Fatalf("unexpected omitted count: %+v", plan)
			}
			for index, name := range testCase.tools {
				if plan.Calls[index].ToolName != name {
					t.Fatalf("unexpected tool order: %+v", plan.Calls)
				}
			}
			if testCase.healthArg != "" && !containsAny(string(plan.Calls[len(plan.Calls)-1].Arguments), []string{testCase.healthArg}) {
				t.Fatalf("unexpected health arguments: %s", plan.Calls[len(plan.Calls)-1].Arguments)
			}
		})
	}
}

func TestPlannerMapsOnlyExplicitOfficialDocumentationRequests(t *testing.T) {
	cases := []struct {
		message    string
		documentID string
	}{
		{message: "Go context 取消传播的官方文档", documentID: "go_context_cancel"},
		{message: "Redis ACL 规范依据", documentID: "redis_acl"},
		{message: "RabbitMQ DLX 官方文档", documentID: "rabbitmq_dlx"},
		{message: "Prometheus 告警的文档证据", documentID: "prometheus_alerting"},
	}
	for _, testCase := range cases {
		call, ok := officialDocumentationCall(strings.ToLower(testCase.message))
		if !ok || call.ToolName != "official_document_search" || !strings.Contains(string(call.Arguments), `"document_id":"`+testCase.documentID+`"`) {
			t.Fatalf("unexpected official documentation mapping for %q: %+v", testCase.message, call)
		}
	}
	if _, ok := officialDocumentationCall("解释 redis acl"); ok {
		t.Fatal("implicit network lookup must not be planned")
	}
}

func TestPlannerUsesOnlyFixedLogArguments(t *testing.T) {
	plan, err := NewPlanner().Plan("查看 MCP timeout 日志")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != "execute" || len(plan.Calls) != 1 || plan.Calls[0].ToolName != "bounded_log_signature" || string(plan.Calls[0].Arguments) != `{"service":"mcp","signature":"timeout"}` {
		t.Fatalf("unexpected MCP log plan: %+v", plan)
	}
}

func TestPlannerRejectsEmptyAndOversizedInput(t *testing.T) {
	planner := NewPlanner()
	if _, err := planner.Plan(" "); err == nil {
		t.Fatal("empty input must fail")
	}
	oversized := make([]rune, 2001)
	for index := range oversized {
		oversized[index] = '问'
	}
	if _, err := planner.Plan(string(oversized)); err == nil {
		t.Fatal("oversized input must fail")
	}
}

func TestPlannerRepairKeepsExactToolNameAndRequiresProgress(t *testing.T) {
	planner := NewPlanner()
	feedback := RepairFeedback{CallIndex: 0, Attempt: 1, ToolName: "deployment_manifest_lookup", ErrorCode: "TOOL_ARGUMENTS_INVALID", RejectedArgsHash: "hash"}
	repaired, err := planner.Repair("查询当前发布清单", PlannedCall{ToolName: "deployment_manifest_lookup", Arguments: json.RawMessage(`{"path":"/tmp"}`)}, feedback)
	if err != nil || repaired.ToolName != "deployment_manifest_lookup" || string(repaired.Arguments) != `{}` {
		t.Fatalf("unexpected exact repair: call=%+v err=%v", repaired, err)
	}
	_, err = planner.Repair("查询当前发布清单", PlannedCall{ToolName: "deployment_manifest_looku", Arguments: json.RawMessage(`{"path":"/tmp"}`)}, feedback)
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("misspelled tool name must not be guessed: %v", err)
	}
	_, err = planner.Repair("查询当前发布清单", PlannedCall{ToolName: "deployment_manifest_lookup", Arguments: json.RawMessage("{\n}")}, feedback)
	if !errors.Is(err, ErrRepairNoProgress) {
		t.Fatalf("identical candidate must be no-progress: %v", err)
	}
}
