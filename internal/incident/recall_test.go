package incident

import (
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/model"
)

func TestRankResolvedIncidentsEnforcesBoundaryRankingAndTopK(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	query := diagnostic.ExtractedInput{ErrorSignatures: []string{"redis_noauth"}, Components: []string{"redis"}}
	candidates := []model.ResolvedIncident{
		caseCandidate("exact-old", "tenant-a", "user-a", StatusConfirmed, IndexStatusIndexed, `["redis_noauth"]`, `["redis"]`, now.Add(-time.Hour)),
		caseCandidate("signature-only", "tenant-a", "user-a", StatusConfirmed, IndexStatusIndexed, `["redis_noauth"]`, `["docker"]`, now),
		caseCandidate("other-user", "tenant-a", "user-b", StatusConfirmed, IndexStatusIndexed, `["redis_noauth"]`, `["redis"]`, now),
		caseCandidate("other-tenant", "tenant-b", "user-a", StatusConfirmed, IndexStatusIndexed, `["redis_noauth"]`, `["redis"]`, now),
		caseCandidate("pending-index", "tenant-a", "user-a", StatusConfirmed, IndexStatusPending, `["redis_noauth"]`, `["redis"]`, now),
		caseCandidate("not-confirmed", "tenant-a", "user-a", "draft", IndexStatusIndexed, `["redis_noauth"]`, `["redis"]`, now),
		caseCandidate("malformed", "tenant-a", "user-a", StatusConfirmed, IndexStatusIndexed, `{`, `["redis"]`, now),
	}

	result := rankResolvedIncidents("tenant-a", "user-a", query, candidates, 2)
	if len(result) != 2 {
		t.Fatalf("expected exactly two eligible results, got %#v", result)
	}
	if result[0].IncidentID != "exact-old" || result[0].Score != 1 {
		t.Fatalf("exact signature and component match must rank first: %#v", result)
	}
	if result[1].IncidentID != "signature-only" || result[1].Score != 0.8 {
		t.Fatalf("signature-only match must rank second: %#v", result)
	}
}

func TestRankResolvedIncidentsRejectsComponentOnlyMatch(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	query := diagnostic.ExtractedInput{ErrorSignatures: []string{"redis_noauth"}, Components: []string{"redis"}}
	candidate := caseCandidate("component-only", "tenant", "user", StatusConfirmed, IndexStatusIndexed, `["redis_wrongtype"]`, `["redis"]`, now)
	if result := rankResolvedIncidents("tenant", "user", query, []model.ResolvedIncident{candidate}, 3); len(result) != 0 {
		t.Fatalf("component-only similarity is below the safe recall boundary: %#v", result)
	}
}

func caseCandidate(id string, tenant string, user string, status string, indexStatus string, signatures string, components string, confirmedAt time.Time) model.ResolvedIncident {
	return model.ResolvedIncident{
		ID: id, TenantIDHash: tenant, UserIDHash: user, Status: status, IndexStatus: indexStatus,
		Symptom: "Redis 返回 NOAUTH", RootCause: "客户端认证配置不匹配", Resolution: "修正认证配置并验证 PONG",
		ErrorSignaturesJSON: signatures, ComponentsJSON: components, ConfirmedAt: confirmedAt,
	}
}
