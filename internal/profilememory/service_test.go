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
	tenantHash       string
	userHash         string
	sourceRun        string
	facts            []capturedFact
	items            []model.EnvironmentMemory
	corrected        model.EnvironmentMemory
	deletedID        string
	recallTenantHash string
	recallUserHash   string
	recallItems      []model.EnvironmentMemory
}

func (repository *memoryRepository) Capture(_ context.Context, tenantHash string, userHash string, sourceRun string, facts []capturedFact, _ time.Time) error {
	repository.tenantHash, repository.userHash, repository.sourceRun = tenantHash, userHash, sourceRun
	repository.facts = append([]capturedFact(nil), facts...)
	return nil
}

func (repository *memoryRepository) List(context.Context, string) ([]model.EnvironmentMemory, error) {
	return append([]model.EnvironmentMemory(nil), repository.items...), nil
}

func (repository *memoryRepository) RecallActive(_ context.Context, tenantHash string, userHash string, _ time.Time) ([]model.EnvironmentMemory, error) {
	repository.recallTenantHash, repository.recallUserHash = tenantHash, userHash
	return append([]model.EnvironmentMemory(nil), repository.recallItems...), nil
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

func TestRecallUsesScopedActiveFreshRelevantTopK(t *testing.T) {
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	future := now.Add(time.Hour)
	repository := &memoryRepository{recallItems: []model.EnvironmentMemory{
		{ID: "redis-active", Key: "redis_version", Value: "7.4", Status: StatusActive, Confidence: 1, ExpiresAt: &future, LastObservedAt: now},
		{ID: "redis-expired", Key: "redis_version", Value: "6.0", Status: StatusActive, Confidence: 1, ExpiresAt: &expired, LastObservedAt: now.Add(time.Minute)},
		{ID: "redis-candidate", Key: "redis_version", Value: "7.5", Status: StatusCandidate, Confidence: 1, LastObservedAt: now},
		{ID: "mysql-active", Key: "mysql_version", Value: "8.0", Status: StatusActive, Confidence: 1, LastObservedAt: now},
	}}
	service, _ := NewService(repository, fixedClock{now: now})
	result, err := service.Recall(context.Background(), "tenant-a", "alice", "Redis NOAUTH 怎么排查", 5)
	if err != nil {
		t.Fatal(err)
	}
	if repository.recallTenantHash != harness.PrincipalHash("tenant-a") || repository.recallUserHash != harness.PrincipalHash("alice") {
		t.Fatalf("recall crossed principal boundary: %+v", repository)
	}
	if result.Status != "hit" || len(result.Items) != 1 || result.Items[0].ID != "redis-active" {
		t.Fatalf("stale/candidate/unrelated memory crossed recall gate: %#v", result)
	}
}
