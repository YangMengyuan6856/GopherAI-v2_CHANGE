package intent

import "testing"

func TestPatternRecognizerHighConfidenceRules(t *testing.T) {
	recognizer := NewPatternRecognizer()
	tests := []struct {
		name    string
		input   PatternInput
		intent  string
		reason  string
		matched bool
	}{
		{"error", PatternInput{Question: "容器启动报错 CONNECTION_REFUSED，帮我定位原因"}, Troubleshooting, "error_signature", true},
		{"log", PatternInput{Question: "日志如下\n2026-09-04 level=error redis unavailable"}, Troubleshooting, "log_block", true},
		{"document", PatternInput{Question: "请重新索引这份项目文档"}, DocTask, "explicit_document_action", true},
		{"tool", PatternInput{Question: "请调用工具检查服务健康状态"}, ToolTask, "explicit_governed_tool_request", true},
		{"explicit knowledge", PatternInput{Question: "默认值是多少", KnowledgeRequired: true}, ProjectQA, "explicit_knowledge_mode", true},
		{"project qa", PatternInput{Question: "根据项目文档说明 GopherAI 支持哪些格式"}, ProjectQA, "project_evidence_request", true},
		{"follow up", PatternInput{Question: "那第二种原因怎么验证？", PreviousIntent: Troubleshooting}, FollowUp, "contextual_follow_up", true},
		{"general", PatternInput{Question: "你好"}, General, "casual_general", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := recognizer.Recognize(test.input)
			if got.Result.Intent != test.intent || got.Result.Stages[0].ReasonCode != test.reason || got.Matched != test.matched {
				t.Fatalf("unexpected decision: %+v", got)
			}
		})
	}
}

func TestPatternRecognizerConflictNeedsFusionAndKeepsSeverePriority(t *testing.T) {
	got := NewPatternRecognizer().Recognize(PatternInput{Question: "重新索引失败并报错 INDEX_BUILD_FAILED，请调用工具检查服务健康状态"})
	if got.Result.Intent != Troubleshooting || !got.Result.IsCompound || got.Matched || got.Result.Confidence > 0.79 {
		t.Fatalf("unsafe conflict decision: %+v", got)
	}
	if got.Result.Stages[0].ReasonCode != "pattern_conflict_requires_fusion" || len(got.CandidateSet) < 2 {
		t.Fatalf("missing conflict diagnostics: %+v", got)
	}
}

func TestPatternRecognizerDoesNotTreatBareKeywordsAsFinalIntent(t *testing.T) {
	for _, question := range []string{"文档", "Redis", "工具", "部署"} {
		got := NewPatternRecognizer().Recognize(PatternInput{Question: question})
		if got.Matched || got.Result.Confidence >= 0.90 {
			t.Fatalf("bare keyword %q was over-classified: %+v", question, got)
		}
	}
}

func TestPatternRecognizerRequiresContextForFollowUp(t *testing.T) {
	got := NewPatternRecognizer().Recognize(PatternInput{Question: "那第二种怎么验证？"})
	if got.Result.Intent != FollowUp || got.Matched || got.Result.Confidence >= 0.60 {
		t.Fatalf("missing-context follow-up should not short-circuit: %+v", got)
	}
}
