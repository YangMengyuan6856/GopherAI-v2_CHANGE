package evaluation

import (
	"GopherAI/internal/intent"
	"bytes"
	"testing"
	"time"
)

func TestEvaluatePatternSeparatesCoverageFromAccuracy(t *testing.T) {
	cases := []IntentCase{
		{ID: "a", Question: "你好", Expected: IntentExpected{Intent: intent.General}, ReviewedBy: "human"},
		{ID: "b", Question: "一个没有明确规则的请求", Expected: IntentExpected{Intent: intent.General}, ReviewedBy: "human"},
		{ID: "c", Question: "容器报 CONNECTION_REFUSED", Expected: IntentExpected{Intent: intent.Troubleshooting}, ReviewedBy: "pending_user"},
	}
	report := EvaluatePattern(cases, "candidate", time.Unix(1, 0))
	if report.MatchedCount != 2 || report.CorrectMatchedCount != 2 || report.Coverage != float64(2)/3 || report.SelectiveAccuracy != 1 {
		t.Fatalf("unexpected selective metrics: %+v", report)
	}
	if report.HumanReviewed || report.BaselineEligible || report.G4Evaluated {
		t.Fatalf("unsafe baseline status: %+v", report)
	}
	var markdown bytes.Buffer
	if err := WriteIntentPatternMarkdown(&markdown, report); err != nil || !bytes.Contains(markdown.Bytes(), []byte("not end-to-end")) {
		t.Fatalf("unexpected markdown: %v %s", err, markdown.String())
	}
}
