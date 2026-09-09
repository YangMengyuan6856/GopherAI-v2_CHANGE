package controlwebhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const updateNotificationCooldown = 15 * time.Minute

type IncidentCandidate struct {
	BatchID      string
	CollectedAt  time.Time
	RulesVersion string
	RulesSHA256  string
	Series       observability.ProductionMetricAnalysis
}

type ReconcileResult struct {
	Examined int `json:"examined"`
	Queued   int `json:"queued"`
	Opened   int `json:"opened"`
	Updated  int `json:"updated"`
	Resolved int `json:"resolved"`
}

type IncidentRepository interface {
	ApplyCandidate(context.Context, IncidentCandidate, time.Time) (string, error)
}

type Reconciler struct {
	repository IncidentRepository
	observer   DeliveryObserver
	clock      func() time.Time
}

func NewReconciler(repository IncidentRepository, observers ...DeliveryObserver) *Reconciler {
	result := &Reconciler{repository: repository, clock: time.Now}
	if len(observers) > 0 {
		result.observer = observers[0]
	}
	return result
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, snapshot observability.ProductionAnomalySnapshot) (ReconcileResult, error) {
	result := ReconcileResult{}
	if reconciler == nil || reconciler.repository == nil || reconciler.clock == nil {
		return result, errors.New("control incident reconciler is not configured")
	}
	if snapshot.Simulation || len(snapshot.BatchID) != 64 || len(snapshot.RulesSHA256) != 64 || snapshot.RulesVersion == "" {
		return result, errors.New("control incident snapshot provenance is invalid")
	}
	now := reconciler.clock().UTC()
	var joined error
	for _, series := range snapshot.Series {
		if series.Analysis == nil || series.DataStatus != observability.MetricWindowObserved {
			continue
		}
		result.Examined++
		eventType, err := reconciler.repository.ApplyCandidate(ctx, IncidentCandidate{
			BatchID: snapshot.BatchID, CollectedAt: snapshot.CollectedAt.UTC(), RulesVersion: snapshot.RulesVersion,
			RulesSHA256: snapshot.RulesSHA256, Series: series,
		}, now)
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		if eventType == "" {
			continue
		}
		if reconciler.observer != nil {
			reconciler.observer.RecordWebhookDelivery(eventType, "queued")
		}
		result.Queued++
		switch eventType {
		case EventOpened:
			result.Opened++
		case EventUpdated:
			result.Updated++
		case EventResolved:
			result.Resolved++
		}
	}
	return result, joined
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) ApplyCandidate(ctx context.Context, candidate IncidentCandidate, now time.Time) (string, error) {
	if repository == nil || repository.db == nil {
		return "", gorm.ErrInvalidDB
	}
	if err := validateCandidate(candidate); err != nil {
		return "", err
	}
	var emitted string
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key := stableIncidentKey(candidate.Series.Metric, candidate.Series.Strategy)
		var incident model.ControlIncident
		findResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("incident_key = ?", key).Limit(1).Find(&incident)
		if findResult.Error != nil {
			return findResult.Error
		}
		exists := findResult.RowsAffected == 1
		eventType, next, notify := transitionIncident(incident, exists, candidate, now)
		if !exists && next == nil {
			return nil
		}
		if !exists {
			if err := tx.Create(next).Error; err != nil {
				return err
			}
		} else if next != nil {
			if err := tx.Model(&model.ControlIncident{}).Where("incident_key = ? AND version = ?", incident.IncidentKey, incident.Version).Updates(map[string]any{
				"status": next.Status, "severity": next.Severity, "last_decision": next.LastDecision,
				"last_batch_id": next.LastBatchID, "last_rules_version": next.LastRulesVersion,
				"last_rules_sha256": next.LastRulesSHA256, "last_value": next.LastValue,
				"last_population": next.LastPopulation, "recovery_streak": next.RecoveryStreak,
				"opened_at": next.OpenedAt, "last_notified_at": next.LastNotifiedAt, "resolved_at": next.ResolvedAt,
				"version": incident.Version + 1, "updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
		if !notify {
			return nil
		}
		delivery, err := buildDelivery(eventType, *next, candidate, now)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery).Error; err != nil {
			return err
		}
		emitted = eventType
		return nil
	})
	return emitted, err
}

func validateCandidate(candidate IncidentCandidate) error {
	analysis := candidate.Series.Analysis
	if len(candidate.BatchID) != 64 || candidate.RulesVersion == "" || len(candidate.RulesSHA256) != 64 || candidate.CollectedAt.IsZero() ||
		candidate.Series.Metric == "" || candidate.Series.Strategy == "" || analysis == nil || analysis.Recommendation.Applied ||
		math.IsNaN(candidate.Series.Latest.Value) || math.IsInf(candidate.Series.Latest.Value, 0) || candidate.Series.Latest.Population < 0 {
		return errors.New("control incident candidate is invalid")
	}
	switch analysis.DecisionStatus {
	case "anomalous", "healthy", "insufficient_data", "insufficient_window":
		return nil
	default:
		return errors.New("control incident decision is invalid")
	}
}

func transitionIncident(current model.ControlIncident, exists bool, candidate IncidentCandidate, now time.Time) (string, *model.ControlIncident, bool) {
	analysis := candidate.Series.Analysis
	if analysis.DecisionStatus != "anomalous" && analysis.DecisionStatus != "healthy" {
		return "", nil, false
	}
	key := stableIncidentKey(candidate.Series.Metric, candidate.Series.Strategy)
	severity := detectorSeverity(*analysis)
	if analysis.DecisionStatus == "anomalous" {
		if !exists || current.Status == IncidentResolved {
			next := model.ControlIncident{
				IncidentKey: key, Metric: candidate.Series.Metric, Strategy: candidate.Series.Strategy,
				Status: IncidentActive, Severity: severity, LastDecision: analysis.DecisionStatus,
				LastBatchID: candidate.BatchID, LastRulesVersion: candidate.RulesVersion, LastRulesSHA256: candidate.RulesSHA256,
				LastValue: candidate.Series.Latest.Value, LastPopulation: int64(candidate.Series.Latest.Population),
				Version: 1, OpenedAt: now, LastNotifiedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			return EventOpened, &next, true
		}
		if current.LastBatchID == candidate.BatchID {
			return "", nil, false
		}
		next := current
		next.Severity, next.LastDecision, next.LastBatchID = severity, analysis.DecisionStatus, candidate.BatchID
		next.LastRulesVersion, next.LastRulesSHA256 = candidate.RulesVersion, candidate.RulesSHA256
		next.LastValue, next.LastPopulation, next.RecoveryStreak, next.UpdatedAt = candidate.Series.Latest.Value, int64(candidate.Series.Latest.Population), 0, now
		notify := now.Sub(current.LastNotifiedAt) >= updateNotificationCooldown || severity != current.Severity
		if notify {
			next.LastNotifiedAt = now
			return EventUpdated, &next, true
		}
		return "", &next, false
	}
	if !exists || current.Status != IncidentActive || current.LastBatchID == candidate.BatchID {
		return "", nil, false
	}
	next := current
	next.LastDecision, next.LastBatchID = analysis.DecisionStatus, candidate.BatchID
	next.LastRulesVersion, next.LastRulesSHA256 = candidate.RulesVersion, candidate.RulesSHA256
	next.LastValue, next.LastPopulation, next.RecoveryStreak, next.UpdatedAt = candidate.Series.Latest.Value, int64(candidate.Series.Latest.Population), current.RecoveryStreak+1, now
	if next.RecoveryStreak < 2 {
		return "", &next, false
	}
	next.Status, next.Severity, next.LastNotifiedAt, next.RecoveryStreak = IncidentResolved, "healthy", now, 0
	resolvedAt := now
	next.ResolvedAt = &resolvedAt
	return EventResolved, &next, true
}

func buildDelivery(eventType string, incident model.ControlIncident, candidate IncidentCandidate, now time.Time) (model.ControlWebhookDelivery, error) {
	if !validEventType(eventType) {
		return model.ControlWebhookDelivery{}, errors.New("control webhook event type is invalid")
	}
	eventID := stableID(SchemaVersion, incident.IncidentKey, eventType, candidate.BatchID)
	analysis := candidate.Series.Analysis
	payload := Payload{
		SchemaVersion: SchemaVersion, EventID: eventID, EventType: eventType, IncidentKey: incident.IncidentKey, OccurredAt: now,
		Scope:       Scope{Metric: candidate.Series.Metric, Strategy: candidate.Series.Strategy},
		Observation: Observation{Value: candidate.Series.Latest.Value, Population: candidate.Series.Latest.Population, WindowSeconds: candidate.Series.WindowSeconds, ObservedAt: candidate.Series.Latest.ObservedAt.UTC()},
		Detector: Detector{
			Version: analysis.DetectorVersion, DecisionStatus: analysis.DecisionStatus,
			FixedStatus: analysis.Fixed.Status, FixedReason: analysis.Fixed.ReasonCode,
			ZScoreStatus: analysis.ZScore.Status, ZScoreReason: analysis.ZScore.ReasonCode,
			ZeroVariance: analysis.ZScore.ZeroVariance, Recommendation: analysis.Recommendation.Action, Applied: false,
		},
		Provenance: Provenance{BatchID: candidate.BatchID, RulesVersion: candidate.RulesVersion, RulesSHA256: candidate.RulesSHA256},
		Guardrails: []string{"recommend_only", "active_policy_unchanged", "no_automatic_tool_execution"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	digest := sha256.Sum256(body)
	return model.ControlWebhookDelivery{
		EventID: eventID, IncidentKey: incident.IncidentKey, EventType: eventType, PayloadJSON: string(body),
		PayloadSHA256: hex.EncodeToString(digest[:]), Status: StatusPending, AvailableAt: now,
		SignatureVersion: SignatureVersion, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func detectorSeverity(analysis observability.AnomalyAnalysis) string {
	if analysis.Fixed.Status == "critical" {
		return "critical"
	}
	return "warning"
}

func stableIncidentKey(metric string, strategy string) string {
	digest := sha256.Sum256([]byte(observability.AnomalyDetectorVersion + "|" + strings.TrimSpace(metric) + "|" + strings.TrimSpace(strategy)))
	return "incident-" + hex.EncodeToString(digest[:])[:16]
}

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}
