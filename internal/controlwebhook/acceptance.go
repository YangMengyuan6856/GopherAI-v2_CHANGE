package controlwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const AcceptanceFixtureMode = "signed_loopback_delivery"

func BuildAcceptanceDelivery(runtime observability.PrometheusRuntimeSnapshot, now time.Time) (model.ControlWebhookDelivery, error) {
	if runtime.Status != "ready" || runtime.RulesVersion == "" || len(runtime.RulesSHA256) != 64 {
		return model.ControlWebhookDelivery{}, errors.New("production Prometheus provenance is not ready")
	}
	policy, observations, err := observability.AcceptanceAnomalyScenario("quality_drop", now)
	if err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	analysis, err := observability.AnalyzeMetricWindow(policy, observations)
	if err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	batchID := stableID("webhook-acceptance", now.UTC().Format(time.RFC3339Nano), runtime.RulesSHA256)
	candidate := IncidentCandidate{
		BatchID: batchID, CollectedAt: now.UTC(), RulesVersion: runtime.RulesVersion, RulesSHA256: runtime.RulesSHA256,
		Series: observability.ProductionMetricAnalysis{
			Metric: policy.Metric, Strategy: policy.Strategy, WindowSeconds: 900, DataStatus: observability.MetricWindowObserved,
			Latest:   observability.MetricWindowPoint{Metric: policy.Metric, Strategy: policy.Strategy, DataStatus: observability.MetricWindowObserved, Value: observations[len(observations)-1].Value, Population: observations[len(observations)-1].Population, WindowSeconds: 900, ObservedAt: now.UTC()},
			Analysis: &analysis,
		},
	}
	incident := model.ControlIncident{IncidentKey: "incident-acceptance-fixture", Metric: policy.Metric, Strategy: policy.Strategy}
	delivery, err := buildDelivery(EventOpened, incident, candidate, now.UTC())
	if err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	var payload Payload
	if err := json.Unmarshal([]byte(delivery.PayloadJSON), &payload); err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	payload.Simulation, payload.FixtureMode = true, AcceptanceFixtureMode
	body, err := json.Marshal(payload)
	if err != nil {
		return model.ControlWebhookDelivery{}, err
	}
	delivery.Simulation, delivery.PayloadJSON, delivery.PayloadSHA256 = true, string(body), stablePayloadSHA(string(body))
	return delivery, nil
}

func (repository *GormRepository) EnqueueAcceptanceDelivery(ctx context.Context, delivery model.ControlWebhookDelivery) error {
	if repository == nil || repository.db == nil || !delivery.Simulation || len(delivery.EventID) != 64 || delivery.IncidentKey != "incident-acceptance-fixture" || delivery.Status != StatusPending || !strings.Contains(delivery.PayloadJSON, `"fixture_mode":"`+AcceptanceFixtureMode+`"`) {
		return gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("webhook acceptance event already exists")
	}
	return nil
}
