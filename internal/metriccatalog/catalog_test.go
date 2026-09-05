package metriccatalog_test

import (
	"testing"

	"GopherAI/internal/knowledge"
	"GopherAI/internal/metriccatalog"
	"GopherAI/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

func TestCatalogDiscoversEveryRegisteredApplicationCollector(t *testing.T) {
	backendRegistry := prometheus.NewRegistry()
	backend := observability.NewMetrics(backendRegistry, backendRegistry)
	workerRegistry := prometheus.NewRegistry()
	worker := knowledge.NewWorkerMetrics(workerRegistry)
	report, err := metriccatalog.Audit(
		metriccatalog.CollectorSet{Component: "backend", Collectors: backend.Collectors()},
		metriccatalog.CollectorSet{Component: "index_worker", Collectors: worker.Collectors()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.FamilyCount != 88 || report.RequiredFamilyCount != 46 || report.RequiredPresentCount != 46 || report.ContractMismatchCount != 0 || report.ForbiddenLabelHits != 0 || report.DuplicateMetricNames != 0 || len(report.CatalogSHA256) != 64 {
		t.Fatalf("metric catalog audit did not pass: %+v", report)
	}
	components := map[string]int{}
	for _, component := range report.Components {
		components[component.Name] = component.FamilyCount
	}
	if components["backend"] != 82 || components["index_worker"] != 6 {
		t.Fatalf("unexpected component coverage: %+v", components)
	}
	if report.LabelKeyCount < 20 || report.MaxSeriesEstimate < report.FamilyCount || report.MaxSeriesEstimate > report.SeriesBudget {
		t.Fatalf("invalid cardinality summary: %+v", report)
	}
	families, err := backendRegistry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	exposed := map[string]bool{}
	for _, family := range families {
		exposed[family.GetName()] = true
	}
	for _, definition := range report.Definitions {
		if definition.Required && !exposed[definition.Name] {
			t.Fatalf("required metric %s is registered but absent from /metrics before first event", definition.Name)
		}
	}
}

func TestCatalogRejectsUnboundedAndDuplicateMetrics(t *testing.T) {
	unbounded := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gopherai_bad_total", Help: "bad"}, []string{"user_id"})
	report, err := metriccatalog.Audit(
		metriccatalog.CollectorSet{Component: "backend", Collectors: []prometheus.Collector{unbounded, unbounded}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.ForbiddenLabelHits != 2 || report.DuplicateMetricNames != 1 || len(report.Violations) < 3 {
		t.Fatalf("unsafe catalog unexpectedly passed: %+v", report)
	}
}
