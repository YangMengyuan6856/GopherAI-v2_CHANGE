package toolruntime

import (
	"strings"
	"testing"
)

func TestFormatToolsForLLMIsDeterministic(t *testing.T) {
	tools := []ToolInfo{
		{
			Name:        "search_logs",
			Description: "检索日志",
			Parameters: map[string]string{
				"query":   "检索条件",
				"service": "服务名称",
			},
			Required: []string{"service"},
		},
		{
			Name:        "read_runbook",
			Description: "读取排障手册",
		},
	}

	want := strings.Join([]string{
		"1. read_runbook - 读取排障手册",
		"2. search_logs - 检索日志",
		"   参数:",
		"   - query: 检索条件",
		"   - service: 服务名称 (必填)",
		"",
	}, "\n")

	for i := 0; i < 20; i++ {
		if got := FormatToolsForLLM(tools); got != want {
			t.Fatalf("unexpected formatted tool list:\n%s", got)
		}
	}

	if tools[0].Name != "search_logs" {
		t.Fatal("FormatToolsForLLM must not mutate the caller's tool order")
	}
}
