package toolagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion     = "tool-plan-v1"
	PlannerVersion    = "bounded-tool-planner-v1"
	MaxPlanCalls      = 2
	MaxRepairAttempts = 2
)

var (
	ErrInvalidRequest    = errors.New("tool agent request is invalid")
	ErrRepairUnavailable = errors.New("candidate repair is unavailable")
	ErrRepairNoProgress  = errors.New("candidate repair made no progress")
)

type PlannedCall struct {
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	ReasonCode  string          `json:"reason_code"`
	EvidenceRef string          `json:"expected_evidence"`
}

type Plan struct {
	SchemaVersion  string        `json:"schema_version"`
	PlannerVersion string        `json:"planner_version"`
	Decision       string        `json:"decision"`
	ReasonCode     string        `json:"reason_code"`
	Calls          []PlannedCall `json:"calls"`
	OmittedCount   int           `json:"omitted_count"`
}

// RepairFeedback contains only stable, sanitized runtime output. A future LLM
// planner may consume it without receiving credentials, raw tool output or an
// internal stack trace.
type RepairFeedback struct {
	CallIndex        int    `json:"call_index"`
	Attempt          int    `json:"attempt"`
	ToolName         string `json:"tool_name"`
	ErrorCode        string `json:"error_code"`
	RejectedArgsHash string `json:"rejected_args_hash"`
}

// CandidatePlanner is the boundary between planning and governed execution.
// Implementations may propose arguments, but the runtime remains authoritative
// for exact tool lookup, schema validation, permissions and side effects.
type CandidatePlanner interface {
	Plan(message string) (Plan, error)
	Repair(message string, current PlannedCall, feedback RepairFeedback) (PlannedCall, error)
}

type Planner struct{}

func NewPlanner() *Planner { return &Planner{} }

// Plan is a bounded control-plane planner, not hidden reasoning. It emits only
// allowlisted tool names, fixed argument enums and stable reason codes.
func (planner *Planner) Plan(message string) (Plan, error) {
	message = strings.TrimSpace(message)
	if message == "" || utf8.RuneCountInString(message) > 2000 {
		return Plan{}, ErrInvalidRequest
	}
	normalized := strings.ToLower(message)
	plan := Plan{SchemaVersion: SchemaVersion, PlannerVersion: PlannerVersion, Decision: "answer_without_tool", ReasonCode: "NO_SUPPORTED_TOOL", Calls: []PlannedCall{}}
	if containsAny(normalized, []string{"删除", "重启", "停止服务", "kill ", "rm -", "执行 shell", "运行 shell", "写入数据库", "update ", "delete from", "drop table", "部署新版本"}) {
		plan.Decision, plan.ReasonCode = "refuse", "UNSAFE_ACTION_REQUESTED"
		return plan, nil
	}

	wantsManifest := containsAny(normalized, []string{"部署清单", "发布清单", "当前发布", "当前版本", "git sha", "commit", "构建方式", "构建目标", "回滚策略", "分支"})
	wantsMCPManifest := wantsManifest && containsAny(normalized, []string{"mcp", "协议源", "协议调用"})
	wantsHealth := containsAny(normalized, []string{"健康", "状态", "是否正常", "ready", "live", "backend", "后端", "worker", "索引服务", "mysql", "redis", "rabbitmq", "依赖"})
	wantsLogs := containsAny(normalized, []string{"日志", " log", "报错", "错误", "panic", "noauth", "unauthorized", "超时", "timeout", "connection refused", "连接拒绝", "slow sql"})
	if wantsManifest {
		if wantsMCPManifest {
			plan.Calls = append(plan.Calls, PlannedCall{ToolName: "mcp_deployment_evidence", Arguments: json.RawMessage(`{}`), ReasonCode: "MCP_RELEASE_EVIDENCE_REQUIRED", EvidenceRef: "mcp:deployment_manifest_source:<release_id>"})
		} else {
			plan.Calls = append(plan.Calls, PlannedCall{ToolName: "deployment_manifest_lookup", Arguments: json.RawMessage(`{}`), ReasonCode: "RELEASE_EVIDENCE_REQUIRED", EvidenceRef: "release-manifest:<release_id>"})
		}
	}
	if documentationCall, ok := officialDocumentationCall(normalized); ok {
		plan.Calls = append(plan.Calls, documentationCall)
	}
	if wantsHealth {
		service := healthTarget(normalized)
		arguments, _ := json.Marshal(map[string]string{"service": service, "probe": "ready"})
		plan.Calls = append(plan.Calls, PlannedCall{ToolName: "service_health_snapshot", Arguments: arguments, ReasonCode: "RUNTIME_HEALTH_EVIDENCE_REQUIRED", EvidenceRef: "health-probe:" + service + ":ready"})
	}
	if wantsLogs {
		service := logTarget(normalized)
		signature := logSignature(normalized)
		arguments, _ := json.Marshal(map[string]string{"service": service, "signature": signature})
		plan.Calls = append(plan.Calls, PlannedCall{ToolName: "bounded_log_signature", Arguments: arguments, ReasonCode: "LOG_SIGNATURE_EVIDENCE_REQUIRED", EvidenceRef: "log-signature:" + service + ":" + signature + ":<content-hash>"})
	}
	if len(plan.Calls) > MaxPlanCalls {
		plan.OmittedCount = len(plan.Calls) - MaxPlanCalls
		plan.Calls = plan.Calls[:MaxPlanCalls]
	}
	if len(plan.Calls) > 0 {
		plan.Decision, plan.ReasonCode = "execute", "SUPPORTED_READ_ONLY_EVIDENCE"
	}
	return plan, nil
}

func officialDocumentationCall(message string) (PlannedCall, bool) {
	if !containsAny(message, []string{"官方文档", "official docs", "official documentation", "规范依据", "文档证据"}) {
		return PlannedCall{}, false
	}
	documentID := ""
	query := ""
	switch {
	case containsAny(message, []string{"redis", "noauth", "acl", "auth"}):
		documentID, query = "redis_acl", "AUTH ACL"
	case containsAny(message, []string{"rabbitmq", "rabbit", "dlq", "dlx", "死信"}):
		documentID, query = "rabbitmq_dlx", "dead letter exchange"
	case containsAny(message, []string{"prometheus", "promql", "告警", "alert"}):
		documentID, query = "prometheus_alerting", "alerting rules"
	case containsAny(message, []string{"context", "cancellation", "cancel", "取消传播", "截止时间", "deadline"}):
		documentID, query = "go_context_cancel", "context cancel"
	default:
		return PlannedCall{}, false
	}
	arguments, _ := json.Marshal(map[string]string{"document_id": documentID, "query": query})
	return PlannedCall{
		ToolName: "official_document_search", Arguments: arguments, ReasonCode: "OFFICIAL_DOCUMENT_EVIDENCE_REQUIRED",
		EvidenceRef: "official-doc:" + documentID + ":<content-hash>",
	}, true
}

// Repair never changes the exact tool name. The deterministic planner rebuilds
// the requested call from the user's original message; future model-backed
// planners can implement the same contract and are still capped by the caller.
func (planner *Planner) Repair(message string, current PlannedCall, feedback RepairFeedback) (PlannedCall, error) {
	if feedback.ErrorCode != "TOOL_ARGUMENTS_INVALID" || feedback.Attempt < 1 || feedback.Attempt > MaxRepairAttempts {
		return PlannedCall{}, ErrRepairUnavailable
	}
	rebuilt, err := planner.Plan(message)
	if err != nil {
		return PlannedCall{}, err
	}
	for _, candidate := range rebuilt.Calls {
		if candidate.ToolName != current.ToolName {
			continue
		}
		if sameJSON(candidate.Arguments, current.Arguments) {
			return PlannedCall{}, ErrRepairNoProgress
		}
		return candidate, nil
	}
	return PlannedCall{}, ErrRepairUnavailable
}

func sameJSON(left json.RawMessage, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func healthTarget(message string) string {
	wantsWorker := containsAny(message, []string{"worker", "索引服务", "索引 worker"})
	wantsBackend := containsAny(message, []string{"backend", "后端", "mysql", "redis", "rabbitmq", "依赖"})
	if wantsWorker && !wantsBackend {
		return "index_worker"
	}
	if wantsBackend && !wantsWorker {
		return "backend"
	}
	return "all"
}

func logTarget(message string) string {
	if containsAny(message, []string{"mcp", "协议宿主"}) {
		return "mcp"
	}
	if containsAny(message, []string{"worker", "索引服务", "索引 worker"}) {
		return "index_worker"
	}
	return "backend"
}

func logSignature(message string) string {
	switch {
	case containsAny(message, []string{"panic", "runtime error", "fatal"}):
		return "panic"
	case containsAny(message, []string{"noauth", "unauthorized", "forbidden", "认证", "鉴权", "jwt", "token"}):
		return "auth"
	case containsAny(message, []string{"timeout", "超时", "deadline", "context canceled", "context cancelled"}):
		return "timeout"
	case containsAny(message, []string{"connection refused", "连接拒绝", "connection reset", "broken pipe", "dial tcp", "no route"}):
		return "connection"
	case containsAny(message, []string{"warning", "warn", "slow sql", "慢查询", "degraded"}):
		return "warning"
	default:
		return "error"
	}
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
