package intent

import "testing"

func TestLabelsAreStableAndCopied(t *testing.T) {
	got := Labels()
	if len(got) != 6 || got[0] != ProjectQA || got[5] != General {
		t.Fatalf("unexpected labels: %v", got)
	}
	got[0] = "changed"
	if Labels()[0] != ProjectQA {
		t.Fatal("Labels returned mutable package state")
	}
}

func TestSevereMisrouteRubric(t *testing.T) {
	tests := []struct {
		expected  string
		predicted string
		severe    bool
	}{
		{Troubleshooting, General, true},
		{Troubleshooting, ProjectQA, true},
		{ToolTask, ProjectQA, true},
		{ProjectQA, General, true},
		{DocTask, General, false},
		{General, ProjectQA, false},
		{ToolTask, ToolTask, false},
	}
	for _, test := range tests {
		if got := IsSevereMisroute(test.expected, test.predicted); got != test.severe {
			t.Fatalf("IsSevereMisroute(%q, %q)=%v want %v", test.expected, test.predicted, got, test.severe)
		}
	}
}
