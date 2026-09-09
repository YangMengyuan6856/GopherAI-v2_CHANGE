package observability

import (
	"context"
	"errors"
	"sort"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
)

const ProductionAnomalySchemaVersion = "production-anomaly-snapshot-v1"

type ProductionMetricAnalysis struct {
	Metric            string            `json:"metric"`
	Strategy          string            `json:"strategy"`
	WindowSeconds     int               `json:"window_seconds"`
	DataStatus        string            `json:"data_status"`
	HistoryPointCount int               `json:"history_point_count"`
	Latest            MetricWindowPoint `json:"latest"`
	Analysis          *AnomalyAnalysis  `json:"analysis,omitempty"`
}

type ProductionAnomalySnapshot struct {
	SchemaVersion string                     `json:"schema_version"`
	Status        string                     `json:"status"`
	Source        string                     `json:"source"`
	Simulation    bool                       `json:"simulation"`
	BatchID       string                     `json:"batch_id,omitempty"`
	CollectedAt   time.Time                  `json:"collected_at,omitempty"`
	RulesVersion  string                     `json:"rules_version,omitempty"`
	RulesSHA256   string                     `json:"rules_sha256,omitempty"`
	Series        []ProductionMetricAnalysis `json:"series"`
	Guardrails    []string                   `json:"guardrails"`
	Limitations   []string                   `json:"limitations"`
}

func (service *MetricWindowService) LatestProductionAnalysis(ctx context.Context) (ProductionAnomalySnapshot, error) {
	result := ProductionAnomalySnapshot{
		SchemaVersion: ProductionAnomalySchemaVersion, Status: "warming", Source: "mysql_metric_window_snapshots",
		Simulation: false, Series: []ProductionMetricAnalysis{},
		Guardrails:  []string{"只读取固定 production metric scope", "检测器不会写 active policy", "不会自动切流或执行修复", "不同 recording-rule 版本的点不会混合"},
		Limitations: []string{"每分钟采样一次，初始窗口不足时保持未决。", "当前仅接入 RAG grounded rate 与协作诊断 P95 两个代表性指标。"},
	}
	if service == nil || service.repository == nil {
		return result, errors.New("metric window repository is not configured")
	}
	latest, err := service.repository.LatestBatch(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, errors.New("load latest metric window batch failed")
	}
	if len(latest) != len(ProductionMetricWindowSpecs()) {
		return result, errors.New("latest metric window batch is incomplete")
	}
	result.BatchID, result.CollectedAt = latest[0].BatchID, latest[0].CollectedAt.UTC()
	result.RulesVersion, result.RulesSHA256 = latest[0].RulesVersion, latest[0].RulesSHA256
	latestByScope := make(map[string]model.MetricWindowSnapshot, len(latest))
	for _, record := range latest {
		if record.BatchID != result.BatchID || record.RulesVersion != result.RulesVersion || record.RulesSHA256 != result.RulesSHA256 {
			return result, errors.New("latest metric window batch provenance is inconsistent")
		}
		latestByScope[record.Metric+"|"+record.Strategy] = record
	}
	overallStatus := "ready"
	for _, spec := range ProductionMetricWindowSpecs() {
		record, exists := latestByScope[spec.Metric+"|"+spec.Strategy]
		if !exists {
			return result, errors.New("latest metric window scope is missing")
		}
		series := ProductionMetricAnalysis{
			Metric: spec.Metric, Strategy: spec.Strategy, WindowSeconds: spec.WindowSeconds,
			DataStatus: record.DataStatus, Latest: PublicMetricWindowPoint(record),
		}
		if record.DataStatus != MetricWindowObserved {
			overallStatus = "warming"
			result.Series = append(result.Series, series)
			continue
		}
		history, historyErr := service.repository.RecentObserved(ctx, spec.Metric, spec.Strategy, record.RulesVersion, record.RulesSHA256, spec.Policy.WindowSize+spec.Policy.ConsecutivePoints)
		if historyErr != nil {
			return result, errors.New("load metric window history failed")
		}
		observations := make([]MetricObservation, 0, len(history))
		for _, point := range history {
			observations = append(observations, MetricObservation{ObservedAt: point.ObservedAt.UTC(), Value: point.Value, Population: int(point.Population)})
		}
		analysis, analysisErr := AnalyzeMetricWindow(spec.Policy, observations)
		if analysisErr != nil {
			return result, errors.New("production metric window analysis failed")
		}
		series.HistoryPointCount, series.Analysis = len(observations), &analysis
		if analysis.DecisionStatus == "anomalous" {
			overallStatus = "anomalous"
		} else if overallStatus != "anomalous" && analysis.DecisionStatus != "healthy" {
			overallStatus = "warming"
		}
		result.Series = append(result.Series, series)
	}
	sort.SliceStable(result.Series, func(left, right int) bool { return result.Series[left].Metric < result.Series[right].Metric })
	result.Status = overallStatus
	return result, nil
}
