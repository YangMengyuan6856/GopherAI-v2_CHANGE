package humanreviewimport

import (
	"os"
	"path/filepath"
	"testing"

	"GopherAI/internal/catalogreview"
)

func TestApprovedCommitmentsMatchUnchangedCasesInRevisedCatalog(t *testing.T) {
	path := filepath.Join("..", "..", "evals", "adjudications", "devsupport-human-approved-r1.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadApprovedCaseManifest(file, "0a228ed89eca7ae5c5c3d1ae4efdbc89e4d8fed7bef7a05c2d7f2fb949c8b56a")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cases) != 240 {
		t.Fatalf("unexpected commitment count: %d", len(manifest.Cases))
	}
	snapshot, err := catalogreview.NewFileArtifactStore(filepath.Join("..", "..", "evals", "devsupport-eval-v1.manifest.json")).Load()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]string, len(snapshot.Cases))
	for _, item := range snapshot.Cases {
		byID[item.ID] = item.CaseSHA256
	}
	for _, commitment := range manifest.Cases {
		if byID[commitment.CaseID] != commitment.CaseSHA256 {
			t.Fatalf("approved case changed and cannot be carried: ordinal=%d id=%s", commitment.Ordinal, commitment.CaseID)
		}
	}
}
