package profilememory

import (
	"context"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/model"
)

type memoryRepository struct {
	tenantHash string
	userHash   string
	sourceRun  string
	facts      []capturedFact
	items      []model.EnvironmentMemory
	corrected  model.EnvironmentMemory
	deletedID  string
}

func (repository *memoryRepository) Capture(_ context.Context, tenantHash string, userHash string, sourceRun string, facts []capturedFact, _ time.Time) error {
	repository.tenantHash, repository.userHash, repository.sourceRun = tenantHash, userHash, sourceRun
	repository.facts = append([]capturedFact(nil), facts...)
	return nil
}

func (repository *memoryRepository) List(context.Context, string) ([]model.EnvironmentMemory, error) {
	return append([]model.EnvironmentMemory(nil), repository.items...), nil
}

func (repository *memoryRepository) Correct(_ context.Context, userHash string, id string, value string, expiresAt *time.Time, now time.Time) (model.EnvironmentMemory, error) {
	repository.userHash = userHash
	repository.corrected = model.EnvironmentMemory{ID: id + "-v2", Key: "redis_version", Value: value, SourceType: SourceUserCorrected, Confidence: 1, Status: StatusActive, Version: 2, ExpiresAt: expiresAt, LastObservedAt: now, UpdatedAt: now}
	return repository.corrected, nil
}

func (repository *memoryRepository) Delete(_ context.Context, userHash string, id string) error {
	repository.userHash, repository.deletedID = userHash, id
	return nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestCaptureAdmitsOnlyAllowlistedStructuredFactsAndHashesPrincipals(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, fixedClock{now: time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	extracted := diagnostic.ExtractedInput{KnownEnvironment: []diagnostic.EnvironmentFact{
		{Key: "redis_version", Value: "7.2", Confidence: 0.7},
		{Key: "arbitrary_user_key", Value: "must-not-persist", Confidence: 1},
	}}
	if err := service.Capture(context.Background(), "tenant-a", "alice", "run-1", extracted); err != nil {
		t.Fatal(err)
	}
	if repository.tenantHash != harness.PrincipalHash("tenant-a") || repository.userHash != harness.PrincipalHash("alice") || repository.sourceRun != "run-1" {
		t.Fatalf("profile principal/source boundary failed: %+v", repository)
	}
	if len(repository.facts) != 1 || repository.facts[0].Key != "redis_version" {
		t.Fatalf("untrusted profile key crossed allowlist: %#v", repository.facts)
	}
}

func TestCorrectionSanitizesSecretAndProducesActiveUserFact(t *testing.T) {
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	repository := new(memoryRepository)
	service, _ := NewService(repository, fixedClock{now: now})
	result, err := service.Correct(context.Background(), Correction{MemoryID: "memory-1", UserID: "alice", Value: "Redis 7.4 password=do-not-store"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Value, "do-not-store") || !strings.Contains(result.Value, "[REDACTED]") {
		t.Fatalf("profile correction was not sanitized: %#v", result)
	}
	if result.Status != StatusActive || result.Confidence != 1 || result.ExpiresAt == nil || !result.ExpiresAt.Equal(now.Add(ConfirmedTTL)) {
		t.Fatalf("user-corrected profile contract failed: %#v", result)
	}
}

func TestListReportsCandidateConflictAndActiveCounts(t *testing.T) {
	repository := &memoryRepository{items: []model.EnvironmentMemory{{Status: StatusCandidate}, {Status: StatusConflicted}, {Status: StatusActive}}}
	service, _ := NewService(repository, fixedClock{now: time.Now()})
	result, err := service.List(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 1 || result.ConflictCount != 1 || result.ActiveCount != 1 || len(result.Items) != 3 {
		t.Fatalf("unexpected profile counts: %#v", result)
	}
}
