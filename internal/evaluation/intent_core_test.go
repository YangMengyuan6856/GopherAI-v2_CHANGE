package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntentDatasetIsBalancedAndPendingHumanReview(t *testing.T) {
	path := filepath.Join("..", "..", "evals", "devsupport-intent-v1.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cases, summary, err := LoadIntentDataset(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 150 || summary.CaseCount != 150 || summary.HumanReviewed {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.CompoundCount < 10 || summary.DifficultyCount["hard"] < 40 {
		t.Fatalf("dataset lacks compound/hard coverage: %+v", summary)
	}
}

func TestIntentDatasetRejectsInvalidFollowUpContext(t *testing.T) {
	row := `{"id":"same","question":"继续","context":{"previous_intent":"general"},"expected":{"intent":"follow_up","is_compound":false},"difficulty":"easy","reviewed_by":"pending_user","dataset_version":"devsupport-intent-v1"}`
	_, _, err := LoadIntentDataset(strings.NewReader(strings.Repeat(row+"\n", 150)))
	if err == nil || !strings.Contains(err.Error(), "previous_intent") {
		t.Fatalf("expected previous intent validation error, got %v", err)
	}
}
