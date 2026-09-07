package evalgovernance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const catalogSHA = "6f5cff1cfb3c0c683ac5241fd9ceb117337833085626e4b8785d80f8b7108cd7"

func TestLoadFileValidatesDatasetSourcesAndTruthClasses(t *testing.T) {
	manifest, err := LoadFile("../../evals/devsupport-eval-v1.governance.json", "devsupport-eval-v1", catalogSHA)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != SchemaVersion || len(manifest.ManifestSHA256) != 64 || len(manifest.Slices) != 6 || manifest.DatasetCard.SourceCategory != "curated_synthetic_contract" {
		t.Fatalf("unexpected governance manifest: %+v", manifest)
	}
	rag, exists := manifest.Slice("rag")
	if !exists || rag.TruthType != "fixture_grounded" || len(rag.SourceReferences) != 2 {
		t.Fatalf("RAG governance missing: %+v", rag)
	}
	truth, exists := manifest.TruthClass(rag.TruthType)
	if !exists || truth.Title == "" {
		t.Fatalf("truth class missing: %+v", truth)
	}
}

func TestLoadFileRejectsCatalogMismatchAndTamperedSource(t *testing.T) {
	path := "../../evals/devsupport-eval-v1.governance.json"
	if _, err := LoadFile(path, "devsupport-eval-v2", catalogSHA); !errors.Is(err, ErrInvalidGovernance) {
		t.Fatalf("dataset mismatch was accepted: %v", err)
	}
	if _, err := LoadFile(path, "devsupport-eval-v1", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"); !errors.Is(err, ErrInvalidGovernance) {
		t.Fatalf("catalog mismatch was accepted: %v", err)
	}

	temporary := t.TempDir()
	copyFile(t, path, filepath.Join(temporary, "governance.json"))
	copyFile(t, "../../evals/intent-rubric-v1.md", filepath.Join(temporary, "intent-rubric-v1.md"))
	copyFile(t, "../../evals/fixtures/kb-fixture-v2.json", filepath.Join(temporary, "fixtures", "kb-fixture-v2.json"))
	copyFile(t, "../../evals/rubrics/devsupport-contract-rubric-v1.md", filepath.Join(temporary, "rubrics", "devsupport-contract-rubric-v1.md"))
	copyFile(t, "../../evals/rubrics/judge-human-scoring-rubric-v1.md", filepath.Join(temporary, "rubrics", "judge-human-scoring-rubric-v1.md"))
	if err := os.WriteFile(filepath.Join(temporary, "intent-rubric-v1.md"), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(filepath.Join(temporary, "governance.json"), "devsupport-eval-v1", catalogSHA); !errors.Is(err, ErrInvalidGovernance) {
		t.Fatalf("tampered source was accepted: %v", err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
