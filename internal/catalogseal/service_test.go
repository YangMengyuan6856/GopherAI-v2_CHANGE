package catalogseal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/evaluation"
)

type reviewSourceStub struct {
	snapshot catalogreview.Snapshot
	progress catalogreview.Progress
	err      error
}

func (stub *reviewSourceStub) SealSnapshot(context.Context, string) (catalogreview.Snapshot, catalogreview.Progress, error) {
	return stub.snapshot, stub.progress, stub.err
}

func TestSealRequiresAllApprovedAndMaterializesValidatedImmutableCatalog(t *testing.T) {
	root := filepath.Join("..", "..")
	catalogPath := filepath.Join(root, DefaultCatalogPath)
	reviewPath := filepath.Join(root, DefaultReviewManifestPath)
	snapshot, err := catalogreview.NewFileArtifactStore(catalogPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC) }
	source := &reviewSourceStub{snapshot: snapshot, progress: catalogreview.Progress{
		Total: 320, Pending: 320, ReviewSetSHA256: strings.Repeat("a", 64),
	}}
	service := NewService(source, catalogPath, reviewPath, t.TempDir(), clock)
	status, err := service.Status(context.Background(), "reviewer")
	if err != nil || status.Eligible || status.Status != "blocked_human_review" || status.Progress.Pending != 320 {
		t.Fatalf("unexpected blocked status: err=%v status=%+v", err, status)
	}
	command := Command{CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA256: source.progress.ReviewSetSHA256, Acknowledgment: Acknowledgment}
	if _, err := service.Seal(context.Background(), "reviewer", command); !errors.Is(err, ErrReviewIncomplete) {
		t.Fatalf("expected incomplete rejection, got %v", err)
	}

	source.progress = catalogreview.Progress{
		Total: 320, Reviewed: 320, Approved: 320, ReviewSetSHA256: strings.Repeat("b", 64), ReadyForMaterializing: true,
	}
	command.ReviewSetSHA256 = source.progress.ReviewSetSHA256
	receipt, err := service.Seal(context.Background(), "reviewer", command)
	if err != nil || !receipt.Created || receipt.Reused || receipt.Report.CaseCount != 320 || receipt.Report.ApprovedCases != 320 {
		t.Fatalf("unexpected seal receipt: err=%v receipt=%+v", err, receipt)
	}
	artifactRoot := filepath.Join(service.outputRoot, receipt.Report.SealID)
	validated, err := ValidateArtifact(artifactRoot)
	if err != nil || validated.SealSHA256 != receipt.Report.SealSHA256 || len(validated.Files) != 11 {
		t.Fatalf("sealed artifact validation failed: err=%v report=%+v", err, validated)
	}
	catalog, err := evaluation.ValidateEvalCatalogFile(filepath.Join(artifactRoot, "evals", sealedCatalogName))
	if err != nil || !catalog.Passed || catalog.Slices[0].ReviewCounts["human"] != 150 || catalog.Slices[0].ReviewCounts["pending_user"] != 0 {
		t.Fatalf("sealed catalog is not human materialized: err=%v report=%+v", err, catalog)
	}
	review, err := evaluation.ValidateReviewManifestFile(filepath.Join(artifactRoot, "evals", sealedReviewName), filepath.Join(artifactRoot, "evals", sealedCatalogName))
	if err != nil || !review.Passed || !review.BaselineEligible || review.ReviewedCases != 320 {
		t.Fatalf("sealed review manifest is not eligible: err=%v report=%+v", err, review)
	}
	reused, err := service.Seal(context.Background(), "reviewer", command)
	if err != nil || reused.Created || !reused.Reused || reused.Report.SealSHA256 != receipt.Report.SealSHA256 {
		t.Fatalf("seal replay was not idempotent: err=%v receipt=%+v", err, reused)
	}
	sealedStatus, err := service.Status(context.Background(), "reviewer")
	if err != nil || sealedStatus.CurrentSeal == nil || sealedStatus.Status != "sealed_candidate_ready" {
		t.Fatalf("sealed status missing: err=%v status=%+v", err, sealedStatus)
	}
	original, err := evaluation.ValidateEvalCatalogFile(catalogPath)
	if err != nil || !original.Passed || original.Slices[0].ReviewCounts["pending_user"] != 150 {
		t.Fatalf("source catalog was mutated: err=%v report=%+v", err, original)
	}
}

func TestSealRejectsStaleCommitmentAndTamperedArtifact(t *testing.T) {
	root := filepath.Join("..", "..")
	catalogPath := filepath.Join(root, DefaultCatalogPath)
	reviewPath := filepath.Join(root, DefaultReviewManifestPath)
	snapshot, err := catalogreview.NewFileArtifactStore(catalogPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	source := &reviewSourceStub{snapshot: snapshot, progress: catalogreview.Progress{
		Total: 320, Reviewed: 320, Approved: 320, ReviewSetSHA256: strings.Repeat("c", 64), ReadyForMaterializing: true,
	}}
	service := NewService(source, catalogPath, reviewPath, t.TempDir(), time.Now)
	if _, err := service.Seal(context.Background(), "reviewer", Command{CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA256: strings.Repeat("d", 64), Acknowledgment: Acknowledgment}); !errors.Is(err, ErrStaleReview) {
		t.Fatalf("expected stale review rejection, got %v", err)
	}
	receipt, err := service.Seal(context.Background(), "reviewer", Command{CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA256: source.progress.ReviewSetSHA256, Acknowledgment: Acknowledgment})
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(service.outputRoot, receipt.Report.SealID)
	path := filepath.Join(artifactRoot, filepath.FromSlash(receipt.Report.Files[0].Path))
	if err := os.WriteFile(path, []byte("tampered\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(artifactRoot); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
}

func TestValidateArtifactRejectsUncommittedExtraFile(t *testing.T) {
	root := filepath.Join("..", "..")
	catalogPath := filepath.Join(root, DefaultCatalogPath)
	reviewPath := filepath.Join(root, DefaultReviewManifestPath)
	snapshot, err := catalogreview.NewFileArtifactStore(catalogPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	source := &reviewSourceStub{snapshot: snapshot, progress: catalogreview.Progress{
		Total: 320, Reviewed: 320, Approved: 320, ReviewSetSHA256: strings.Repeat("e", 64), ReadyForMaterializing: true,
	}}
	service := NewService(source, catalogPath, reviewPath, t.TempDir(), time.Now)
	receipt, err := service.Seal(context.Background(), "reviewer", Command{CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA256: source.progress.ReviewSetSHA256, Acknowledgment: Acknowledgment})
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(service.outputRoot, receipt.Report.SealID)
	if err := os.WriteFile(filepath.Join(artifactRoot, "uncommitted.txt"), []byte("not in seal report"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(artifactRoot); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("expected extra file to invalidate artifact, got %v", err)
	}
}
