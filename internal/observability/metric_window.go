package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/model"

	"gorm.io/gorm"
)

const (
	MetricWindowSchemaVersion    = "production-metric-window-v1"
	MetricWindowCollectorVersion = "prometheus-window-collector-v1"
	MetricWindowObserved         = "observed"
	MetricWindowNoSeries         = "no_series"
	MetricWindowNoFiniteValue    = "no_finite_value"
	maximumMetricPopulation      = 1_000_000_000
	metricWindowRetention        = 7 * 24 * time.Hour
)

type MetricWindowSource interface {
	Snapshot(context.Context) (PrometheusRuntimeSnapshot, error)
	QueryFixedMetric(context.Context, FixedPrometheusMetric) (PrometheusInstantSample, error)
}

type MetricWindowRepository interface {
	StoreBatch(context.Context, []model.MetricWindowSnapshot) error
	LatestBatch(context.Context) ([]model.MetricWindowSnapshot, error)
	RecentObserved(context.Context, string, string, string, string, int) ([]model.MetricWindowSnapshot, error)
}

type MetricWindowSpec struct {
	Metric           string                `json:"metric"`
	Strategy         string                `json:"strategy"`
	WindowSeconds    int                   `json:"window_seconds"`
	ValueSource      FixedPrometheusMetric `json:"-"`
	PopulationSource FixedPrometheusMetric `json:"-"`
	Policy           DetectionPolicy       `json:"policy"`
	valueMinimum     float64
	valueMaximum     float64
}

type MetricWindowPoint struct {
	Metric        string    `json:"metric"`
	Strategy      string    `json:"strategy"`
	DataStatus    string    `json:"data_status"`
	Value         float64   `json:"value"`
	Population    int       `json:"population"`
	WindowSeconds int       `json:"window_seconds"`
	ObservedAt    time.Time `json:"observed_at"`
	CollectedAt   time.Time `json:"collected_at"`
	RulesVersion  string    `json:"rules_version"`
	RulesSHA256   string    `json:"rules_sha256"`
}

type MetricWindowBatch struct {
	SchemaVersion    string              `json:"schema_version"`
	CollectorVersion string              `json:"collector_version"`
	Status           string              `json:"status"`
	Source           string              `json:"source"`
	BatchID          string              `json:"batch_id"`
	CollectedAt      time.Time           `json:"collected_at"`
	RulesVersion     string              `json:"rules_version"`
	RulesSHA256      string              `json:"rules_sha256"`
	Points           []MetricWindowPoint `json:"points"`
	Guardrails       []string            `json:"guardrails"`
}

type MetricWindowService struct {
	source     MetricWindowSource
	repository MetricWindowRepository
	clock      func() time.Time
}

func NewMetricWindowService(source MetricWindowSource, repository MetricWindowRepository) *MetricWindowService {
	return &MetricWindowService{source: source, repository: repository, clock: time.Now}
}

func NewDefaultMetricWindowService() *MetricWindowService {
	return NewMetricWindowService(NewDefaultPrometheusRuntimeClient(), NewGormMetricWindowRepository(mysql.DB))
}

func ProductionMetricWindowSpecs() []MetricWindowSpec {
	return []MetricWindowSpec{
		{
			Metric: "rag_grounded_answer_rate", Strategy: "rag_deep", WindowSeconds: 15 * 60,
			ValueSource: PrometheusRAGDeepGroundedRate, PopulationSource: PrometheusRAGDeepPopulation,
			Policy:       DetectionPolicy{Metric: "rag_grounded_answer_rate", Strategy: "rag_deep", Direction: DirectionHigherIsBetter, WarningThreshold: .93, CriticalThreshold: .90, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2},
			valueMinimum: 0, valueMaximum: 1,
		},
		{
			Metric: "request_p95_latency_seconds", Strategy: "diagnosis_collaborative", WindowSeconds: 5 * 60,
			ValueSource: PrometheusCollaborativeRequestP95, PopulationSource: PrometheusCollaborativePopulation,
			Policy:       DetectionPolicy{Metric: "request_p95_latency_seconds", Strategy: "diagnosis_collaborative", Direction: DirectionLowerIsBetter, WarningThreshold: 2, CriticalThreshold: 4, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2},
			valueMinimum: 0, valueMaximum: 300,
		},
	}
}

func (service *MetricWindowService) Capture(ctx context.Context) (MetricWindowBatch, error) {
	if service == nil || service.source == nil || service.repository == nil || service.clock == nil {
		return MetricWindowBatch{}, errors.New("metric window collector is not configured")
	}
	runtimeSnapshot, err := service.source.Snapshot(ctx)
	if err != nil {
		return MetricWindowBatch{}, errors.New("Prometheus runtime validation failed")
	}
	if runtimeSnapshot.Status != "ready" || runtimeSnapshot.RulesVersion != RecordingRulesVersion || len(runtimeSnapshot.RulesSHA256) != 64 {
		return MetricWindowBatch{}, errors.New("Prometheus runtime contract is not ready")
	}
	collectedAt := service.clock().UTC().Truncate(time.Minute)
	batchID := stableMetricWindowID(MetricWindowSchemaVersion, runtimeSnapshot.RulesVersion, runtimeSnapshot.RulesSHA256, collectedAt.Format(time.RFC3339))
	points := make([]MetricWindowPoint, 0, len(ProductionMetricWindowSpecs()))
	records := make([]model.MetricWindowSnapshot, 0, len(ProductionMetricWindowSpecs()))
	for _, spec := range ProductionMetricWindowSpecs() {
		valueSample, queryErr := service.source.QueryFixedMetric(ctx, spec.ValueSource)
		if queryErr != nil {
			return MetricWindowBatch{}, errors.New("Prometheus fixed value query failed")
		}
		populationSample, queryErr := service.source.QueryFixedMetric(ctx, spec.PopulationSource)
		if queryErr != nil {
			return MetricWindowBatch{}, errors.New("Prometheus fixed population query failed")
		}
		point, pointErr := buildMetricWindowPoint(spec, runtimeSnapshot, collectedAt, valueSample, populationSample)
		if pointErr != nil {
			return MetricWindowBatch{}, pointErr
		}
		points = append(points, point)
		records = append(records, model.MetricWindowSnapshot{
			SnapshotKey: stableMetricWindowID(batchID, point.Metric, point.Strategy), BatchID: batchID,
			Metric: point.Metric, Strategy: point.Strategy, DataStatus: point.DataStatus, Value: point.Value,
			Population: int64(point.Population), WindowSeconds: point.WindowSeconds, ObservedAt: point.ObservedAt,
			CollectedAt: point.CollectedAt, RulesVersion: point.RulesVersion, RulesSHA256: point.RulesSHA256,
			Collector: MetricWindowCollectorVersion,
		})
	}
	if err := service.repository.StoreBatch(ctx, records); err != nil {
		return MetricWindowBatch{}, errors.New("metric window persistence failed")
	}
	status := "observed"
	for _, point := range points {
		if point.DataStatus != MetricWindowObserved {
			status = "warming"
			break
		}
	}
	return MetricWindowBatch{
		SchemaVersion: MetricWindowSchemaVersion, CollectorVersion: MetricWindowCollectorVersion,
		Status: status, Source: "prometheus_recording_rules", BatchID: batchID, CollectedAt: collectedAt,
		RulesVersion: runtimeSnapshot.RulesVersion, RulesSHA256: runtimeSnapshot.RulesSHA256, Points: points,
		Guardrails: []string{"固定查询目录，客户端不能提交 PromQL", "整批校验后原子写入 MySQL", "无序列与非有限值显式标记", "请求/用户/租户标识不进入快照", "MySQL 快照仅保留 7 天"},
	}, nil
}

func buildMetricWindowPoint(spec MetricWindowSpec, runtimeSnapshot PrometheusRuntimeSnapshot, collectedAt time.Time, valueSample PrometheusInstantSample, populationSample PrometheusInstantSample) (MetricWindowPoint, error) {
	point := MetricWindowPoint{
		Metric: spec.Metric, Strategy: spec.Strategy, DataStatus: MetricWindowNoSeries,
		WindowSeconds: spec.WindowSeconds, CollectedAt: collectedAt, ObservedAt: collectedAt,
		RulesVersion: runtimeSnapshot.RulesVersion, RulesSHA256: runtimeSnapshot.RulesSHA256,
	}
	if populationSample.Status == PrometheusMetricObserved {
		if populationSample.Value < 0 || populationSample.Value > maximumMetricPopulation || math.IsNaN(populationSample.Value) || math.IsInf(populationSample.Value, 0) {
			return MetricWindowPoint{}, errors.New("Prometheus population violated the fixed metric contract")
		}
		point.Population = int(math.Floor(populationSample.Value + 1e-9))
		point.ObservedAt = populationSample.ObservedAt
	}
	if valueSample.Status == PrometheusMetricObserved {
		if populationSample.Status != PrometheusMetricObserved {
			return MetricWindowPoint{}, errors.New("Prometheus value had no matching population")
		}
		if valueSample.Value < spec.valueMinimum || valueSample.Value > spec.valueMaximum || math.IsNaN(valueSample.Value) || math.IsInf(valueSample.Value, 0) {
			return MetricWindowPoint{}, errors.New("Prometheus value violated the fixed metric contract")
		}
		point.DataStatus, point.Value = MetricWindowObserved, valueSample.Value
		if valueSample.ObservedAt.After(point.ObservedAt) {
			point.ObservedAt = valueSample.ObservedAt
		}
	} else if valueSample.Status == PrometheusMetricNonFinite || populationSample.Status == PrometheusMetricNonFinite {
		point.DataStatus = MetricWindowNoFiniteValue
	}
	return point, nil
}

func stableMetricWindowID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}

func PublicMetricWindowPoint(record model.MetricWindowSnapshot) MetricWindowPoint {
	population := record.Population
	if population < 0 {
		population = 0
	}
	if population > maximumMetricPopulation {
		population = maximumMetricPopulation
	}
	return MetricWindowPoint{
		Metric: record.Metric, Strategy: record.Strategy, DataStatus: record.DataStatus, Value: record.Value,
		Population: int(population), WindowSeconds: record.WindowSeconds, ObservedAt: record.ObservedAt.UTC(),
		CollectedAt: record.CollectedAt.UTC(), RulesVersion: record.RulesVersion, RulesSHA256: record.RulesSHA256,
	}
}

type GormMetricWindowRepository struct{ db *gorm.DB }

func NewGormMetricWindowRepository(db *gorm.DB) *GormMetricWindowRepository {
	return &GormMetricWindowRepository{db: db}
}

func (repository *GormMetricWindowRepository) StoreBatch(ctx context.Context, records []model.MetricWindowSnapshot) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	if len(records) != len(ProductionMetricWindowSpecs()) {
		return errors.New("metric window batch is incomplete")
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.MetricWindowSnapshot{}).Where("batch_id = ?", records[0].BatchID).Count(&count).Error; err != nil {
			return err
		}
		if count == int64(len(records)) {
			return nil
		}
		if count != 0 {
			return errors.New("partial metric window batch already exists")
		}
		if err := tx.Create(&records).Error; err != nil {
			return err
		}
		return tx.Where("collected_at < ?", records[0].CollectedAt.Add(-metricWindowRetention)).Delete(&model.MetricWindowSnapshot{}).Error
	})
}

func (repository *GormMetricWindowRepository) LatestBatch(ctx context.Context) ([]model.MetricWindowSnapshot, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var latest model.MetricWindowSnapshot
	if err := repository.db.WithContext(ctx).Order("collected_at DESC").Order("id DESC").First(&latest).Error; err != nil {
		return nil, err
	}
	var records []model.MetricWindowSnapshot
	if err := repository.db.WithContext(ctx).Where("batch_id = ?", latest.BatchID).Order("metric ASC, strategy ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (repository *GormMetricWindowRepository) RecentObserved(ctx context.Context, metric string, strategy string, rulesVersion string, rulesSHA256 string, limit int) ([]model.MetricWindowSnapshot, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit < 1 || limit > 64 {
		return nil, errors.New("metric window history limit is invalid")
	}
	var records []model.MetricWindowSnapshot
	if err := repository.db.WithContext(ctx).
		Where("metric = ? AND strategy = ? AND rules_version = ? AND rules_sha256 = ? AND data_status = ?", metric, strategy, rulesVersion, rulesSHA256, MetricWindowObserved).
		Order("observed_at DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(left, right int) bool { return records[left].ObservedAt.Before(records[right].ObservedAt) })
	return records, nil
}

// RunMetricWindowSampler performs bounded, periodic captures. Failures are
// logged with stable reason codes and retried on the next interval; they never
// block the HTTP server or mutate routing policy.
func RunMetricWindowSampler(ctx context.Context, service *MetricWindowService, initialDelay time.Duration, interval time.Duration, logger *log.Logger) {
	if service == nil || interval <= 0 {
		return
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		captureContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		batch, err := service.Capture(captureContext)
		cancel()
		if logger != nil {
			if err != nil {
				logger.Print(`{"event":"metric_window_capture","status":"failed","reason_code":"capture_failed"}`)
			} else {
				logger.Printf(`{"event":"metric_window_capture","status":"%s","points":%d}`, batch.Status, len(batch.Points))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
