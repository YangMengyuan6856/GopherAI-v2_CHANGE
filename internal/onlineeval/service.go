package onlineeval

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"time"
)

const AuditSchemaVersion = "online-evaluation-audit-v1"

type PublicSample struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	Intent          string     `json:"intent"`
	Strategy        string     `json:"strategy"`
	PolicyVersion   string     `json:"policy_version"`
	TrafficClass    string     `json:"traffic_class"`
	SampleRateBasis int        `json:"sample_rate_basis"`
	Reasons         []string   `json:"reasons"`
	RedactionCount  int        `json:"redaction_count"`
	Overall         float64    `json:"overall,omitempty"`
	Relevance       float64    `json:"relevance,omitempty"`
	Completeness    float64    `json:"completeness,omitempty"`
	Helpfulness     float64    `json:"helpfulness,omitempty"`
	Groundedness    float64    `json:"groundedness,omitempty"`
	Safety          float64    `json:"safety,omitempty"`
	LastErrorCode   string     `json:"last_error_code,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	EvaluatedAt     *time.Time `json:"evaluated_at,omitempty"`
}

type Audit struct {
	SchemaVersion      string         `json:"schema_version"`
	Mode               string         `json:"mode"`
	SamplerVersion     string         `json:"sampler_version"`
	EvaluatorVersion   string         `json:"evaluator_version"`
	Queue              string         `json:"queue"`
	RetentionDays      int            `json:"retention_days"`
	LowConfidenceBelow float64        `json:"low_confidence_below"`
	Rates              map[string]int `json:"rates_basis_points"`
	ForcedReasons      []string       `json:"forced_sample_reasons"`
	PrivacyGuarantees  []string       `json:"privacy_guarantees"`
	Window             Summary        `json:"last_24_hours"`
	Latest             *PublicSample  `json:"latest,omitempty"`
}

type AcceptanceCase struct {
	Name         string   `json:"name"`
	TrafficClass string   `json:"traffic_class"`
	RateBasis    int      `json:"sample_rate_basis"`
	Forced       bool     `json:"forced"`
	Reasons      []string `json:"reasons"`
	Passed       bool     `json:"passed"`
}

type AcceptanceStage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type AcceptanceReport struct {
	SchemaVersion         string            `json:"schema_version"`
	Simulation            bool              `json:"simulation"`
	ProductionMetricsUsed bool              `json:"production_metrics_used"`
	Passed                bool              `json:"passed"`
	SampleID              string            `json:"sample_id"`
	FinalStatus           string            `json:"final_status"`
	RedactionCount        int               `json:"redaction_count"`
	RawIdentityPersisted  bool              `json:"raw_identity_persisted"`
	Cases                 []AcceptanceCase  `json:"cases"`
	Stages                []AcceptanceStage `json:"stages"`
	Limitations           []string          `json:"limitations"`
}

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{repository: repository, clock: clock}
}

func (service *Service) Audit(ctx context.Context) (Audit, error) {
	if service == nil || service.repository == nil {
		return Audit{}, errors.New("online evaluation repository is unavailable")
	}
	since := service.clock().UTC().Add(-24 * time.Hour)
	summary, err := service.repository.Summary(ctx, since)
	if err != nil {
		return Audit{}, err
	}
	audit := Audit{
		SchemaVersion: AuditSchemaVersion, Mode: "production_async", SamplerVersion: SamplerVersion, EvaluatorVersion: EvaluatorVersion,
		Queue: Topic, RetentionDays: RetentionDays, LowConfidenceBelow: LowConfidenceCutoff,
		Rates:             map[string]int{TrafficStable: StableRateBasis, TrafficCanary: CanaryRateBasis, TrafficProbing: ProbingRateBasis, "risk": ForcedRateBasis},
		ForcedReasons:     []string{"user_downvote", "request_error", "low_confidence", "evidence_gate_failed", "tool_failure", "budget_exceeded", "unresolved"},
		PrivacyGuarantees: []string{"用户与租户标识仅保存 SHA-256", "问题、答案和允许证据入队前脱敏", "API 不返回原始问题、答案或证据正文", "样本到期自动清理"},
		Window:            summary,
	}
	if summary.Latest != nil {
		latest := publicSample(*summary.Latest)
		audit.Latest = &latest
		audit.Window.Latest = nil
	}
	return audit, nil
}

func (service *Service) RunAcceptance(ctx context.Context, userID string) (AcceptanceReport, error) {
	if service == nil || service.repository == nil {
		return AcceptanceReport{}, errors.New("online evaluation repository is unavailable")
	}
	cases := acceptanceCases()
	now := service.clock().UTC()
	output := acceptanceOutput(userID)
	decision := decide(output, nil, "user_downvote")
	sample, event, err := BuildSample(output, nil, decision, now, true)
	if err != nil {
		return AcceptanceReport{}, err
	}
	if err := service.repository.Create(ctx, &sample, &event); err != nil {
		return AcceptanceReport{}, err
	}
	report := AcceptanceReport{
		SchemaVersion: "online-evaluation-acceptance-v1", Simulation: true, ProductionMetricsUsed: false,
		SampleID: sample.ID, FinalStatus: StatusPending, RedactionCount: sample.RedactionCount,
		RawIdentityPersisted: sample.UserHash == userID || sample.TenantHash == userID,
		Cases:                cases,
		Stages:               []AcceptanceStage{{Name: "mysql_outbox", Status: "completed"}, {Name: "rabbitmq", Status: "waiting"}, {Name: "online_eval_consumer", Status: "waiting"}, {Name: "deterministic_completion", Status: "waiting"}},
		Limitations:          []string{"验收样本验证真实 MySQL Outbox、RabbitMQ 和 Worker 消费链路。", "验收样本不调用模型、不写入生产质量指标，也不影响策略权重。"},
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, loadErr := service.repository.Get(ctx, sample.ID)
		if loadErr != nil {
			return report, loadErr
		}
		report.FinalStatus = current.Status
		if current.Status == StatusCompleted {
			report.Stages[1].Status = "completed"
			report.Stages[2].Status = "completed"
			report.Stages[3].Status = "completed"
			report.Passed = allCasesPassed(cases) && current.JudgeModelVersion == "no-model-call" && !report.RawIdentityPersisted && current.RedactionCount >= 3
			return report, nil
		}
		if current.Status == StatusDead || current.Status == StatusJudgeFailed {
			return report, nil
		}
		select {
		case <-ctx.Done():
			return report, nil
		case <-ticker.C:
		}
	}
}

func acceptanceCases() []AcceptanceCase {
	base := acceptanceOutput("acceptance-user")
	items := make([]AcceptanceCase, 0, 7)
	appendCase := func(name string, output app.ChatOutput, force string, expectedRate int, expectedForced bool) {
		decision := decide(output, nil, force)
		items = append(items, AcceptanceCase{Name: name, TrafficClass: decision.TrafficClass, RateBasis: decision.RateBasis, Forced: decision.Forced, Reasons: decision.Reasons, Passed: decision.RateBasis == expectedRate && decision.Forced == expectedForced})
	}
	appendCase("稳定流量 4%", base, "", StableRateBasis, false)
	canary := base
	canary.Decision.ExperimentBucket = "canary-17"
	appendCase("Canary 流量 20%", canary, "", CanaryRateBasis, false)
	probing := base
	probing.Decision.ReasonCode = "probing_exploration"
	appendCase("Probing 流量 50%", probing, "", ProbingRateBasis, false)
	appendCase("用户点踩 100%", base, "user_downvote", ForcedRateBasis, true)
	low := base
	low.Result.Confidence = .42
	appendCase("低置信度 100%", low, "", ForcedRateBasis, true)
	evidence := base
	evidence.Decision.StrategyName = "rag_fast"
	evidence.Result.Evidence = nil
	appendCase("证据门禁失败 100%", evidence, "", ForcedRateBasis, true)
	tool := base
	tool.Result.ToolCalls = []contract.ToolCallResult{{ToolName: "deployment_manifest_lookup", Status: "failed", ErrorCode: "TOOL_FAILED"}}
	appendCase("工具失败 100%", tool, "", ForcedRateBasis, true)
	return items
}

func acceptanceOutput(userID string) app.ChatOutput {
	return app.ChatOutput{
		Request:  contract.RequestContext{TraceID: "acceptance-trace", RequestID: "acceptance-request", UserID: userID, TenantID: userID, Question: "联系 owner@example.com，password=plain-secret，Bearer abcdefghijk123456 后核对发布配置"},
		Intent:   contract.IntentResult{Intent: "project_qa", Version: "acceptance-intent-v1"},
		Decision: contract.StrategyDecision{StrategyName: "rag_fast", StrategyVersion: "acceptance-strategy-v1", PolicyVersion: "acceptance-policy-v1"},
		Result: contract.AgentResult{Answer: "发布配置已经按证据核对，token=another-secret-value。", Confidence: .92, Resolved: true,
			Evidence: []contract.Evidence{{ID: "evidence-1", Kind: "knowledge", TenantID: userID, SourceID: "document-1", SourceVersion: "v1", Title: "部署手册", Content: "授权邮箱 admin@example.com，api_key=secret-key-value。", Retrieval: "hybrid"}}},
	}
}

func publicSample(sample model.OnlineEvaluationSample) PublicSample {
	var reasons []string
	_ = json.Unmarshal([]byte(sample.SampleReasonsJSON), &reasons)
	return PublicSample{
		ID: sample.ID, Status: sample.Status, Intent: sample.Intent, Strategy: sample.Strategy, PolicyVersion: sample.PolicyVersion,
		TrafficClass: sample.TrafficClass, SampleRateBasis: sample.SampleRateBasis, Reasons: reasons, RedactionCount: sample.RedactionCount,
		Overall: sample.Overall, Relevance: sample.Relevance, Completeness: sample.Completeness, Helpfulness: sample.Helpfulness,
		Groundedness: sample.Groundedness, Safety: sample.Safety, LastErrorCode: sample.LastErrorCode, CreatedAt: sample.CreatedAt, EvaluatedAt: sample.EvaluatedAt,
	}
}

func allCasesPassed(cases []AcceptanceCase) bool {
	for _, item := range cases {
		if !item.Passed {
			return false
		}
	}
	return true
}
