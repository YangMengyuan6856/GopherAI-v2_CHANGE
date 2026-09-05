package policy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	"GopherAI/internal/contract"
	"GopherAI/internal/observability"
	policydomain "GopherAI/internal/policy"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const maximumSimulationBodyBytes = 8 * 1024

type Service interface {
	Snapshot(context.Context) (policydomain.PolicySnapshot, error)
	Simulate(context.Context, string, string, map[policydomain.Dependency]bool, contract.ExecutionBudgets) (policydomain.SelectionResult, policydomain.PolicySnapshot, error)
}

type DependencyHealth interface {
	Snapshot(context.Context) map[policydomain.Dependency]bool
}

type Handler struct {
	service Service
	health  DependencyHealth
}

type SimulationRequest struct {
	Intent string `json:"intent"`
}

type PolicyIdentity struct {
	Version       string     `json:"version"`
	Hash          string     `json:"hash"`
	Status        string     `json:"status"`
	Environment   string     `json:"environment"`
	Source        string     `json:"source"`
	CacheDegraded bool       `json:"cache_degraded"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
}

type SnapshotResponse struct {
	SchemaVersion      string                              `json:"schema_version"`
	Mode               string                              `json:"mode"`
	AffectsLiveTraffic bool                                `json:"affects_live_traffic"`
	Notice             string                              `json:"notice"`
	Policy             PolicyIdentity                      `json:"policy"`
	Rules              map[string]policydomain.RoutingRule `json:"rules"`
	Registry           []policydomain.StrategyMetadata     `json:"registry"`
}

type SimulationResponse struct {
	SchemaVersion      string                           `json:"schema_version"`
	Mode               string                           `json:"mode"`
	AffectsLiveTraffic bool                             `json:"affects_live_traffic"`
	Intent             string                           `json:"intent"`
	Policy             PolicyIdentity                   `json:"policy"`
	Dependencies       map[policydomain.Dependency]bool `json:"dependencies"`
	Selection          policydomain.SelectionResult     `json:"selection"`
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(service Service, health DependencyHealth) *Handler {
	return &Handler{service: service, health: health}
}

func NewDefaultHandler() *Handler {
	registry := policydomain.DefaultStrategyRegistry()
	repository := policydomain.NewCachedPolicyRepository(
		policydomain.NewGormPolicyAuthority(mysql.DB),
		policydomain.NewRedisPolicyCache(redisstore.Rdb, 30*time.Second),
	)
	service, err := policydomain.NewStrategyControlService(repository, registry, policydomain.DefaultPolicyEnvironment, policydomain.DefaultRoutingPolicy(), observability.DefaultMetrics())
	if err != nil {
		panic(err)
	}
	return NewHandler(service, defaultDependencyHealth{})
}

func (handler *Handler) Active(ctx *gin.Context) {
	if handler == nil || handler.service == nil {
		handler.writeError(ctx, http.StatusServiceUnavailable, "POLICY_SERVICE_UNAVAILABLE", "策略控制服务暂时不可用", true)
		return
	}
	snapshot, err := handler.service.Snapshot(ctx.Request.Context())
	if err != nil {
		handler.writeError(ctx, http.StatusServiceUnavailable, "ACTIVE_POLICY_UNAVAILABLE", "当前策略暂时不可读取", true)
		return
	}
	ctx.JSON(http.StatusOK, SnapshotResponse{
		SchemaVersion: policydomain.StrategyControlSchemaVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Notice: "当前页面只做固定分桶演算，不会改变真实对话路由。", Policy: policyIdentity(snapshot),
		Rules: snapshot.Document.Rules, Registry: snapshot.Registry,
	})
}

func (handler *Handler) Simulate(ctx *gin.Context) {
	if handler == nil || handler.service == nil || handler.health == nil {
		handler.writeError(ctx, http.StatusServiceUnavailable, "POLICY_SERVICE_UNAVAILABLE", "策略演算服务暂时不可用", true)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumSimulationBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(SimulationRequest)
	if err := decoder.Decode(request); err != nil || ensureEOF(decoder) != nil {
		handler.writeError(ctx, http.StatusBadRequest, "POLICY_SIMULATION_REQUEST_INVALID", "策略演算请求必须是字段受限的 JSON", false)
		return
	}
	request.Intent = strings.TrimSpace(request.Intent)
	if !simulatableIntent(request.Intent) {
		handler.writeError(ctx, http.StatusBadRequest, "POLICY_INTENT_INVALID", "请选择项目问答、故障诊断或通用问答意图", false)
		return
	}
	dependencies := handler.health.Snapshot(ctx.Request.Context())
	userID := strings.TrimSpace(ctx.GetString("userName"))
	if userID == "" {
		handler.writeError(ctx, http.StatusUnauthorized, "POLICY_PRINCIPAL_MISSING", "无法确认当前登录用户", false)
		return
	}
	result, snapshot, err := handler.service.Simulate(ctx.Request.Context(), userID, request.Intent, dependencies, defaultSimulationBudgets())
	if err != nil {
		handler.writeError(ctx, http.StatusServiceUnavailable, "POLICY_SIMULATION_UNAVAILABLE", "当前依赖不足，无法完成策略演算", true)
		return
	}
	ctx.JSON(http.StatusOK, SimulationResponse{
		SchemaVersion: policydomain.StrategyControlSchemaVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Intent: request.Intent, Policy: policyIdentity(snapshot), Dependencies: dependencies, Selection: result,
	})
}

func policyIdentity(snapshot policydomain.PolicySnapshot) PolicyIdentity {
	return PolicyIdentity{
		Version: snapshot.Record.Version, Hash: snapshot.Record.PolicyHash, Status: snapshot.Record.Status,
		Environment: snapshot.Record.Environment, Source: snapshot.Source, CacheDegraded: snapshot.CacheDegraded,
		ActivatedAt: snapshot.Record.ActivatedAt,
	}
}

func defaultSimulationBudgets() contract.ExecutionBudgets {
	return contract.ExecutionBudgets{MaxAgents: 2, MaxToolCalls: 4, MaxIterations: 10, MaxInputTokens: 16_000, MaxOutputTokens: 4_000, MaxCostMicros: 500_000, TotalTimeout: 90 * time.Second}
}

func simulatableIntent(intent string) bool {
	switch intent {
	case "project_qa", "troubleshooting", "general":
		return true
	default:
		return false
	}
}

func (handler *Handler) writeError(ctx *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(status, ErrorResponse{SchemaVersion: policydomain.StrategyControlSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

type defaultDependencyHealth struct{}

func (defaultDependencyHealth) Snapshot(parent context.Context) map[policydomain.Dependency]bool {
	ctx, cancel := context.WithTimeout(parent, 1200*time.Millisecond)
	defer cancel()
	modelReady := modelConfigurationReady()
	mysqlReady := mysqlPing(ctx) == nil
	vectorReady := vectorPing(ctx) == nil
	return map[policydomain.Dependency]bool{
		policydomain.DependencyModel: modelReady, policydomain.DependencyVector: vectorReady,
		policydomain.DependencyTool: mysqlReady, policydomain.DependencyCaseMemory: mysqlReady && vectorReady,
	}
}

func mysqlPing(ctx context.Context) error {
	if mysql.DB == nil {
		return errors.New("mysql is unavailable")
	}
	database, err := mysql.DB.DB()
	if err != nil {
		return err
	}
	return database.PingContext(ctx)
}

func vectorPing(ctx context.Context) error {
	if redisstore.Rdb == nil {
		return errors.New("vector store is unavailable")
	}
	return redisstore.Rdb.Do(ctx, "FT._LIST").Err()
}

func modelConfigurationReady() bool {
	configuration := config.GetConfig()
	values := []string{os.Getenv("OPENAI_API_KEY"), configuration.RagModelConfig.RagBaseUrl, configuration.RagModelConfig.RagChatModelName, configuration.RagModelConfig.RagEmbeddingModel}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}
