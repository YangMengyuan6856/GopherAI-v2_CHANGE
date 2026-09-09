package incident

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Confirm(context.Context, confirmationWrite) (model.ResolvedIncident, bool, error)
	GetByRun(context.Context, string, string) (*model.ResolvedIncident, error)
	GetByID(context.Context, string, string) (*model.ResolvedIncident, error)
	MarkIndexed(context.Context, string, int, time.Time) error
	RecordIndexFailure(context.Context, string, int, string, bool) error
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

const (
	caseRecallCandidateLimit = 100
	caseRecallThreshold      = 0.60
)

// RetrieveSimilar implements diagnostic.CaseRetriever using MySQL as the
// authority. IndexStatusIndexed is a hard eligibility gate: a case cannot be
// recalled until the asynchronous RabbitMQ/Redis indexing pipeline completed.
func (repository *GormRepository) RetrieveSimilar(ctx context.Context, tenantID string, userID string, query diagnostic.ExtractedInput, limit int) ([]diagnostic.SimilarIncident, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	tenantID, userID = strings.TrimSpace(tenantID), strings.TrimSpace(userID)
	if tenantID == "" || userID == "" {
		return nil, errors.New("case recall principal is required")
	}
	tenantHash, userHash := harness.PrincipalHash(tenantID), harness.PrincipalHash(userID)
	var candidates []model.ResolvedIncident
	err := repository.db.WithContext(ctx).
		Where("tenant_id_hash = ? AND user_id_hash = ? AND status = ? AND index_status = ?", tenantHash, userHash, StatusConfirmed, IndexStatusIndexed).
		Order("confirmed_at DESC").Limit(caseRecallCandidateLimit).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	return rankResolvedIncidents(tenantHash, userHash, query, candidates, limit), nil
}

func rankResolvedIncidents(tenantHash string, userHash string, query diagnostic.ExtractedInput, candidates []model.ResolvedIncident, limit int) []diagnostic.SimilarIncident {
	if limit < 1 {
		return nil
	}
	if limit > 3 {
		limit = 3
	}
	result := make([]diagnostic.SimilarIncident, 0, limit)
	for _, candidate := range candidates {
		// Defense in depth: even if the database query is changed later, rows
		// outside the current principal or confirmation boundary stay invisible.
		if candidate.TenantIDHash != tenantHash || candidate.UserIDHash != userHash || candidate.Status != StatusConfirmed || candidate.IndexStatus != IndexStatusIndexed {
			continue
		}
		var signatures, components []string
		if json.Unmarshal([]byte(candidate.ErrorSignaturesJSON), &signatures) != nil || json.Unmarshal([]byte(candidate.ComponentsJSON), &components) != nil {
			continue
		}
		matchedSignatures, signatureScore := signalSimilarity(query.ErrorSignatures, signatures)
		matchedComponents, componentScore := signalSimilarity(query.Components, components)
		score := 0.80*signatureScore + 0.20*componentScore
		if len(matchedSignatures) == 0 || score < caseRecallThreshold {
			continue
		}
		result = append(result, diagnostic.SimilarIncident{
			IncidentID: candidate.ID, Symptom: candidate.Symptom, RootCause: candidate.RootCause, Resolution: candidate.Resolution,
			MatchedErrorSignatures: matchedSignatures, MatchedComponents: matchedComponents, Score: score, ConfirmedAt: candidate.ConfirmedAt,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if !result[i].ConfirmedAt.Equal(result[j].ConfirmedAt) {
			return result[i].ConfirmedAt.After(result[j].ConfirmedAt)
		}
		return result[i].IncidentID < result[j].IncidentID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func signalSimilarity(left []string, right []string) ([]string, float64) {
	leftSet, rightSet := normalizedSet(left), normalizedSet(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return nil, 0
	}
	intersection := make([]string, 0)
	for value := range leftSet {
		if _, exists := rightSet[value]; exists {
			intersection = append(intersection, value)
		}
	}
	sort.Strings(intersection)
	unionSize := len(leftSet) + len(rightSet) - len(intersection)
	return intersection, float64(len(intersection)) / float64(unionSize)
}

func normalizedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}

func (repository *GormRepository) Confirm(ctx context.Context, write confirmationWrite) (model.ResolvedIncident, bool, error) {
	if repository == nil || repository.db == nil {
		return model.ResolvedIncident{}, false, gorm.ErrInvalidDB
	}
	var result model.ResolvedIncident
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		feedback := new(model.ResolutionFeedback)
		err := tx.Where("user_id_hash = ? AND client_request_id = ?", write.Feedback.UserIDHash, write.Feedback.ClientRequestID).First(feedback).Error
		if err == nil {
			if feedback.SourceRunID != write.Feedback.SourceRunID || feedback.HypothesisID != write.Feedback.HypothesisID || feedback.ResolutionSHA256 != write.ResolutionSHA256 {
				return ErrIdempotencyConflict
			}
			return tx.Where("feedback_id = ? AND user_id_hash = ?", feedback.ID, write.Feedback.UserIDHash).First(&result).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		existing := new(model.ResolvedIncident)
		err = tx.Where("user_id_hash = ? AND source_run_id = ?", write.Incident.UserIDHash, write.Incident.SourceRunID).First(existing).Error
		if err == nil {
			if existing.HypothesisID != write.Incident.HypothesisID || existing.Resolution != write.Incident.Resolution {
				return ErrAlreadyConfirmed
			}
			result = *existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		current := new(model.AgentLifecycleRun)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("run_id = ? AND tenant_id_hash = ? AND user_id_hash = ?", write.Run.RunID, write.Run.TenantIDHash, write.Run.UserIDHash).
			First(current).Error; err != nil {
			return err
		}
		if current.State != "SUCCEEDED" || current.State != write.Run.State || current.StateVersion != write.Run.StateVersion {
			return ErrRunNotEligible
		}
		for _, value := range []any{&write.Feedback, &write.Incident, &write.Event} {
			if err := tx.Create(value).Error; err != nil {
				return err
			}
		}
		result, created = write.Incident, true
		return nil
	})
	if err == nil {
		return result, created, nil
	}
	// A concurrent replay can win the unique key race after our initial read.
	// Re-read by idempotency key and only convert it into success if the payload
	// is exactly equivalent.
	feedback := new(model.ResolutionFeedback)
	lookupErr := repository.db.WithContext(ctx).Where("user_id_hash = ? AND client_request_id = ?", write.Feedback.UserIDHash, write.Feedback.ClientRequestID).First(feedback).Error
	if lookupErr == nil {
		if feedback.SourceRunID != write.Feedback.SourceRunID || feedback.HypothesisID != write.Feedback.HypothesisID || feedback.ResolutionSHA256 != write.ResolutionSHA256 {
			return model.ResolvedIncident{}, false, ErrIdempotencyConflict
		}
		lookupErr = repository.db.WithContext(ctx).Where("feedback_id = ? AND user_id_hash = ?", feedback.ID, write.Feedback.UserIDHash).First(&result).Error
		return result, false, lookupErr
	}
	return model.ResolvedIncident{}, false, err
}

func (repository *GormRepository) GetByRun(ctx context.Context, userIDHash string, runID string) (*model.ResolvedIncident, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	incident := new(model.ResolvedIncident)
	err := repository.db.WithContext(ctx).Where("user_id_hash = ? AND source_run_id = ?", userIDHash, runID).First(incident).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return incident, err
}

func (repository *GormRepository) GetByID(ctx context.Context, tenantIDHash string, incidentID string) (*model.ResolvedIncident, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	incident := new(model.ResolvedIncident)
	err := repository.db.WithContext(ctx).Where("tenant_id_hash = ? AND id = ?", tenantIDHash, incidentID).First(incident).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return incident, err
}

func (repository *GormRepository) MarkIndexed(ctx context.Context, incidentID string, version int, indexedAt time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Model(&model.ResolvedIncident{}).
		Where("id = ? AND version = ? AND status = ?", incidentID, version, StatusConfirmed).
		Updates(map[string]any{"index_status": IndexStatusIndexed, "index_error_code": "", "indexed_at": indexedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (repository *GormRepository) RecordIndexFailure(ctx context.Context, incidentID string, version int, code string, exhausted bool) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	status := IndexStatusPending
	if exhausted {
		status = IndexStatusFailed
	}
	return repository.db.WithContext(ctx).Model(&model.ResolvedIncident{}).
		Where("id = ? AND version = ?", incidentID, version).
		Updates(map[string]any{"index_status": status, "index_error_code": code}).Error
}
