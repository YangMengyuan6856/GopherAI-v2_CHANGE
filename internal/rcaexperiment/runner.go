package rcaexperiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/toolruntime"
	"github.com/google/uuid"
)

type Run struct {
	Diagnosis        Diagnosis                 `json:"diagnosis"`
	Observation      Observation               `json:"observation"`
	ToolCalls        []toolruntime.ToolMessage `json:"tool_calls"`
	TraceID          string                    `json:"trace_id"`
	DatasetSHA256    string                    `json:"dataset_sha256"`
	ElapsedMS        float64                   `json:"elapsed_ms"`
	AuditStorage     string                    `json:"audit_storage"`
	LegacySignatures []string                  `json:"legacy_signatures,omitempty"`
}
type snapshotTool struct {
	definition toolruntime.Definition
	execute    func(context.Context) (toolruntime.Output, error)
}

func (t snapshotTool) Definition() toolruntime.Definition { return t.definition }
func (t snapshotTool) Execute(ctx context.Context, _ map[string]any) (toolruntime.Output, error) {
	if err := ctx.Err(); err != nil {
		return toolruntime.Output{}, err
	}
	return t.execute(ctx)
}

func NewSnapshotRegistry(o Observation, refs []Reference) (*toolruntime.Registry, error) {
	registry := toolruntime.NewRegistry()
	for _, kind := range []string{"metrics", "logs", "traces", "cases"} {
		kind := kind
		name := "rca_snapshot_" + kind
		if kind == "cases" {
			name = "rca_reference_cases"
		}
		definition := toolruntime.Definition{Name: name, Version: "1.0.0", Description: "Read-only frozen RCAEval evidence; no production target or arbitrary paths.", InputSchema: toolruntime.InputSchema{Type: "object", Properties: map[string]toolruntime.PropertySchema{"case_id": {Type: "string", Enum: []string{o.ID}}, "target": {Type: "string", Enum: []string{"benchmark"}}}, Required: []string{"case_id", "target"}, AdditionalProperties: false}, AllowedIntents: []string{"rca_experiment"}, RequiredPermission: "rca_experiment:read", SideEffect: toolruntime.SideEffectReadOnly, TimeoutMS: 1000, MaxResultBytes: 1024 * 1024, Idempotent: true, RetryMaxAttempts: 1}
		tool := snapshotTool{definition: definition, execute: func(context.Context) (toolruntime.Output, error) {
			ids := []string{}
			if kind == "cases" {
				items := SearchReferences(o, refs)
				for _, r := range items {
					ids = append(ids, "reference:"+r.ID)
				}
				return toolruntime.Output{Data: items, EvidenceRefs: ids}, nil
			}
			items := make([]Service, 0, len(o.Services))
			for _, s := range o.Services {
				item := Service{Name: s.Name}
				switch kind {
				case "metrics":
					item.Metrics = s.Metrics
					for _, m := range s.Metrics {
						ids = append(ids, m.ID)
					}
				case "logs":
					item.Logs = s.Logs
					if s.Logs.ID != "" {
						ids = append(ids, s.Logs.ID)
					}
				case "traces":
					item.Traces = s.Traces
					if s.Traces.ID != "" {
						ids = append(ids, s.Traces.ID)
					}
				}
				items = append(items, item)
			}
			return toolruntime.Output{Data: items, EvidenceRefs: ids}, nil
		}}
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func Execute(ctx context.Context, d *Dataset, id, strategy, user string, auditor toolruntime.Auditor, observer toolruntime.Observer) (Run, error) {
	started := time.Now()
	out := Run{TraceID: uuid.NewString(), ToolCalls: []toolruntime.ToolMessage{}, DatasetSHA256: d.SHA256, AuditStorage: "local_run_report"}
	if auditor != nil {
		out.AuditStorage = "mysql_tool_audit"
	}
	if strategy != "case_based" && strategy != "feature_only" && strategy != "legacy" {
		return out, fmt.Errorf("unknown strategy")
	}
	original, ok := d.Observation(id)
	if !ok {
		return out, fmt.Errorf("unknown case")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	registry, err := NewSnapshotRegistry(original, d.References)
	if err != nil {
		return out, err
	}
	runtime, err := toolruntime.NewRuntime(registry, auditor, observer)
	if err != nil {
		return out, err
	}
	o := original
	o.Services = nil
	refs := []Reference{}
	names := []string{"rca_snapshot_metrics"}
	if strategy == "case_based" {
		names = append(names, "rca_reference_cases")
	}
	if strategy != "legacy" {
		names = append(names, "rca_snapshot_logs", "rca_snapshot_traces")
	}
	args, _ := json.Marshal(map[string]string{"case_id": id, "target": "benchmark"})
	guard := toolruntime.NewActionGuard()
	for i, name := range names {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		message := runtime.Invoke(ctx, toolruntime.Invocation{CallID: fmt.Sprintf("%s-%d", out.TraceID, i), TraceID: out.TraceID, ToolName: name, Arguments: args, Intent: "rca_experiment", Strategy: strategy, Principal: toolruntime.Principal{TenantID: user, UserID: user, Permissions: map[string]bool{"rca_experiment:read": true}}, AllowedSideEffect: toolruntime.SideEffectReadOnly, Budget: toolruntime.CallBudget{MaxCalls: 4, UsedCalls: i}, ActionGuard: guard})
		out.ToolCalls = append(out.ToolCalls, message)
		if message.Status != toolruntime.StatusSuccess {
			return out, fmt.Errorf("evidence tool failed: %s", message.Status)
		}
		if name == "rca_reference_cases" {
			if err = json.Unmarshal(message.Data, &refs); err != nil {
				return out, err
			}
			continue
		}
		var services []Service
		if err = json.Unmarshal(message.Data, &services); err != nil {
			return out, err
		}
		if name == "rca_snapshot_metrics" {
			o.Services = services
			continue
		}
		for _, s := range services {
			for j := range o.Services {
				if o.Services[j].Name == s.Name {
					if name == "rca_snapshot_logs" {
						o.Services[j].Logs = s.Logs
					} else {
						o.Services[j].Traces = s.Traces
					}
				}
			}
		}
	}
	if strategy == "legacy" {
		var text strings.Builder
		text.WriteString("Online Boutique 微服务故障观测，以下为原始单位的窗口中位数。\n")
		for _, s := range o.Services {
			for _, dim := range dimensions {
				if m, ok := s.Metrics[dim.Name]; ok {
					fmt.Fprintf(&text, "%s %s %.4g -> %.4g\n", s.Name, dim.Name, m.Reference, m.Current)
				}
			}
		}
		extracted, legacy, e := diagnostic.NewAgent().AnalyzeContext(ctx, text.String())
		if e != nil {
			return out, e
		}
		out.LegacySignatures = extracted.ErrorSignatures
		out.Diagnosis = Diagnosis{CaseID: id, Version: diagnostic.AgentVersion, Strategy: strategy, Status: "insufficient_evidence", Summary: fmt.Sprintf("原文本规则命中 %d 个错误特征、产生 %d 个排查假设，但没有本实验所需的结构化服务/故障类型定位。", len(extracted.ErrorSignatures), len(legacy.Hypotheses)), Candidates: []Candidate{}, References: []Reference{}, RepairStatus: "not_executed", Warnings: []string{"此基线用于记录旧规则的遥测适配缺口，不代表所有规则诊断都无效。"}}
	} else {
		out.Diagnosis = Diagnose(o, refs, strategy)
	}
	out.Observation = o
	out.ElapsedMS = float64(time.Since(started).Microseconds()) / 1000
	return out, nil
}
