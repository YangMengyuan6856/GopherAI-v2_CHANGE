package toolagent

import "testing"

func TestPlannerSelectsBoundedRealTools(t *testing.T) {
	cases := []struct {
		name      string
		message   string
		decision  string
		tools     []string
		healthArg string
	}{
		{name: "manifest", message: "当前发布版本、Git SHA 和回滚策略是什么？", decision: "execute", tools: []string{"deployment_manifest_lookup"}},
		{name: "backend", message: "检查后端 Redis 和 MySQL 是否正常", decision: "execute", tools: []string{"service_health_snapshot"}, healthArg: `"service":"backend"`},
		{name: "worker", message: "索引 Worker ready 状态", decision: "execute", tools: []string{"service_health_snapshot"}, healthArg: `"service":"index_worker"`},
		{name: "compound", message: "给出当前发布清单，并检查后端和 Worker 健康状态", decision: "execute", tools: []string{"deployment_manifest_lookup", "service_health_snapshot"}, healthArg: `"service":"all"`},
		{name: "no tool", message: "解释 Go interface 的用途", decision: "answer_without_tool", tools: []string{}},
		{name: "unsafe", message: "重启后端并删除旧数据库记录", decision: "refuse", tools: []string{}},
		{name: "backend auth logs", message: "查看后端 NOAUTH 报错日志", decision: "execute", tools: []string{"service_health_snapshot", "bounded_log_signature"}, healthArg: `"service":"backend"`},
		{name: "worker warning logs", message: "检索 Worker slow sql 日志", decision: "execute", tools: []string{"service_health_snapshot", "bounded_log_signature"}, healthArg: `"service":"index_worker"`},
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
