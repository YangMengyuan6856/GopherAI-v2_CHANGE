package humanreviewimport

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseRequiresCompleteOrderedHumanConclusions(t *testing.T) {
	var source strings.Builder
	for ordinal := 1; ordinal <= FullCaseCount; ordinal++ {
		if ordinal == 47 {
			fmt.Fprintf(&source, "%03d|REJECTED|原因码：missing_context,expected_result_incorrect|缺少上下文且标签错误。\n", ordinal)
		} else {
			fmt.Fprintf(&source, "%03d|APPROVED|输入、规则与期望结果可以独立核验。\n", ordinal)
		}
	}
	for ordinal := 1; ordinal <= JudgeCaseCount; ordinal++ {
		fmt.Fprintf(&source, "Judge%03d|相关性=1|完整性=0.75|有用性=0.5|有依据=0.25|安全性=0|评分理由完整。\n", ordinal)
	}
	parsed, err := Parse(strings.NewReader(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	summary := parsed.Summary()
	if summary.FullCount != 320 || summary.Approved != 319 || summary.Rejected != 1 || summary.JudgeCount != 30 || summary.ReasonCounts["missing_context"] != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if parsed.Full[46].Decision != "rejected" || parsed.Judge[0].Scores.Completeness != .75 || len(parsed.SourceSHA256) != 64 {
		t.Fatalf("parsed conclusions lost semantics: %+v %+v", parsed.Full[46], parsed.Judge[0])
	}
}

func TestParseFailsClosedOnMissingRowUnknownReasonAndInvalidScore(t *testing.T) {
	valid := validSource()
	if _, err := Parse(strings.NewReader(strings.Replace(valid, "001|APPROVED", "002|APPROVED", 1))); err == nil {
		t.Fatal("misnumbered Full row was accepted")
	}
	if _, err := Parse(strings.NewReader(strings.Replace(valid, "001|APPROVED|", "001|REJECTED|原因码：invented_reason|", 1))); err == nil {
		t.Fatal("unknown rejection reason was accepted")
	}
	if _, err := Parse(strings.NewReader(strings.Replace(valid, "Judge001|相关性=1", "Judge001|相关性=0.3", 1))); err == nil {
		t.Fatal("non-quarter Judge score was accepted")
	}
}

func validSource() string {
	var source strings.Builder
	for ordinal := 1; ordinal <= FullCaseCount; ordinal++ {
		fmt.Fprintf(&source, "%03d|APPROVED|可独立核验。\n", ordinal)
	}
	for ordinal := 1; ordinal <= JudgeCaseCount; ordinal++ {
		fmt.Fprintf(&source, "Judge%03d|相关性=1|完整性=1|有用性=1|有依据=1|安全性=1|可独立评分。\n", ordinal)
	}
	return source.String()
}
