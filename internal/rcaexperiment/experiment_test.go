package rcaexperiment

import (
	"GopherAI/internal/toolruntime"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func dataForTest(t *testing.T) *Dataset {
	t.Helper()
	d, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestDatasetBoundaries(t *testing.T) {
	d := dataForTest(t)
	counts := map[string]int{}
	for _, c := range d.Catalog {
		counts[c.Split]++
		o, ok := d.Observation(c.ID)
		if !ok {
			t.Fatal("missing observation")
		}
		raw, _ := json.Marshal(o)
		var top map[string]json.RawMessage
		_ = json.Unmarshal(raw, &top)
		for _, key := range []string{"fault", "root_cause_service", "inject_time", "supported"} {
			if _, ok := top[key]; ok {
				t.Fatal("answer in observation", key)
			}
		}
		if len(raw) > 1<<20 {
			t.Fatal("oversized observation")
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"reference": 6, "development": 9, "holdout": 12}) {
		t.Fatal(counts)
	}
}
func TestHistoricalScaleAndOpaqueIDInvariance(t *testing.T) {
	d := dataForTest(t)
	var ref Reference
	for _, r := range d.References {
		if r.Service == "currencyservice" && r.Fault == "cpu" {
			ref = r
		}
	}
	o := Observation{ID: "new-observation", Services: []Service{ref.Observation}}
	before := Diagnose(o, nil, "feature_only")
	after := Diagnose(o, SearchReferences(o, d.References), "case_based")
	if len(before.Candidates) != 0 || len(after.Candidates) != 1 || after.Candidates[0].Service != ref.Service || after.Candidates[0].Fault != ref.Fault {
		t.Fatalf("historical amplitude not applied: %v %v", before, after)
	}
	o.ID = "another-opaque-id"
	renamed := Diagnose(o, SearchReferences(o, d.References), "case_based")
	if !reflect.DeepEqual(after.Candidates, renamed.Candidates) {
		t.Fatal("prediction depends on opaque ID")
	}
	if after.RepairStatus != "not_executed" || after.ModelCalls != 0 {
		t.Fatal("false execution claim")
	}
}
func TestMissingOrHealthyEvidenceDoesNotForceNearestCase(t *testing.T) {
	d := dataForTest(t)
	for _, services := range [][]Service{nil, {{Name: "checkoutservice", Metrics: map[string]Metric{"cpu": {Ratio: 1, LogChange: 0}}}}} {
		o := Observation{ID: "unknown", Services: services}
		r := Diagnose(o, SearchReferences(o, d.References), "case_based")
		if len(r.Candidates) != 0 || r.Status != "insufficient_evidence" {
			t.Fatal(r)
		}
	}
}
func TestSnapshotGovernance(t *testing.T) {
	d := dataForTest(t)
	o := d.Observations[0]
	reg, e := NewSnapshotRegistry(o, d.References)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := toolruntime.NewRuntime(reg, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	base := toolruntime.Invocation{CallID: "test-call", ToolName: "rca_snapshot_metrics", Arguments: json.RawMessage(`{"case_id":"` + o.ID + `","target":"benchmark"}`), Intent: "rca_experiment", Strategy: "case_based", Principal: toolruntime.Principal{TenantID: "tester", UserID: "tester", Permissions: map[string]bool{"rca_experiment:read": true}}, AllowedSideEffect: toolruntime.SideEffectReadOnly, Budget: toolruntime.CallBudget{MaxCalls: 4}}
	tests := []struct {
		name   string
		change func(*toolruntime.Invocation)
		status string
	}{
		{"valid", func(*toolruntime.Invocation) {}, toolruntime.StatusSuccess},
		{"production target", func(i *toolruntime.Invocation) {
			i.Arguments = json.RawMessage(`{"case_id":"` + o.ID + `","target":"production"}`)
		}, toolruntime.StatusInvalidArgs},
		{"other case", func(i *toolruntime.Invocation) {
			i.Arguments = json.RawMessage(`{"case_id":"another-case","target":"benchmark"}`)
		}, toolruntime.StatusInvalidArgs},
		{"shell tool", func(i *toolruntime.Invocation) { i.ToolName = "shell" }, toolruntime.StatusRejected},
		{"no permission", func(i *toolruntime.Invocation) { i.Principal.Permissions = nil }, toolruntime.StatusRejected},
		{"exhausted", func(i *toolruntime.Invocation) { i.Budget.UsedCalls = 4 }, toolruntime.StatusBudgetExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := base
			tt.change(&i)
			m := runtime.Invoke(context.Background(), i)
			if m.Status != tt.status {
				t.Fatal(m)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m := runtime.Invoke(ctx, base); m.Status != toolruntime.StatusCancelled {
		t.Fatal(m)
	}
}
func TestReferenceCannotMatchItself(t *testing.T) {
	d := dataForTest(t)
	o, _ := d.Observation(d.References[0].ID)
	for _, r := range SearchReferences(o, d.References) {
		if r.ID == o.ID {
			t.Fatal("self retrieval")
		}
	}
}
