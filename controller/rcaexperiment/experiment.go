package rcaexperimentcontroller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"GopherAI/common/mysql"
	"GopherAI/internal/observability"
	rcaagentplatform "GopherAI/internal/platform/rcaagent"
	"GopherAI/internal/rcaagent"
	"GopherAI/internal/rcaexperiment"
	"GopherAI/internal/rcascoring"
	"GopherAI/internal/toolruntime"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	dataset      *rcaexperiment.Dataset
	gate         chan struct{}
	auditor      toolruntime.Auditor
	observer     toolruntime.Observer
	agentFactory func(context.Context) (*rcaagent.Agent, error)
}

func NewHandler(d *rcaexperiment.Dataset, a toolruntime.Auditor, o toolruntime.Observer) *Handler {
	return &Handler{dataset: d, gate: make(chan struct{}, 1), auditor: a, observer: o}
}
func NewDefaultHandler() *Handler {
	d, err := rcaexperiment.Load()
	if err != nil {
		return NewHandler(nil, nil, nil)
	}
	h := NewHandler(d, toolruntime.NewGormAuditor(mysql.DB), observability.DefaultMetrics())
	h.agentFactory = rcaagentplatform.NewDefaultAgent
	return h
}
func (h *Handler) ready(c *gin.Context) bool {
	if c.GetString("userName") == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "请重新登录"})
		return false
	}
	if h.dataset == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "RCAEval 实验数据暂不可用"})
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}
func (h *Handler) Catalog(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": h.dataset.Version, "revision": h.dataset.Revision, "dataset_sha256": h.dataset.SHA256, "cases": h.dataset.Catalog, "references": h.dataset.References, "supported_services": rcaexperiment.SupportedServices(), "supported_faults": []string{"cpu", "mem", "delay"}, "extractor": h.dataset.Extractor, "max_concurrency": 1,
		"evaluation_contract": "known_fault_autonomous_investigation_v2", "split_counts": gin.H{"reference": 6, "development": 6, "holdout": 6},
		"agent_version": rcaagent.Version, "agent_max_model_calls": rcaagent.MaxRounds, "agent_max_tool_calls": rcaagent.MaxToolCalls, "agent_timeout_seconds": int(rcaagent.TotalTimeout.Seconds())})
}
func (h *Handler) Observation(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	o, ok := h.dataset.Observation(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"message": "案例不存在"})
		return
	}
	c.JSON(http.StatusOK, o)
}
func (h *Handler) Diagnose(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	var request struct {
		CaseID   string `json:"case_id"`
		Strategy string `json:"strategy"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请求只能指定案例和策略"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请求包含多余内容"})
		return
	}
	if request.Strategy != "autonomous" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "该实验仅运行受治理的自主排查 Agent"})
		return
	}
	if _, ok := h.dataset.Case(request.CaseID); !ok {
		c.JSON(http.StatusNotFound, gin.H{"message": "案例不存在"})
		return
	}
	select {
	case h.gate <- struct{}{}:
		defer func() { <-h.gate }()
	default:
		c.JSON(http.StatusTooManyRequests, gin.H{"message": "已有实验正在运行，请稍后再试"})
		return
	}
	if h.agentFactory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "自主排查模型暂不可用；不会切换成规则结果"})
		return
	}
	agent, err := h.agentFactory(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "自主排查模型配置不可用；未调用规则诊断"})
		return
	}
	run, err := agent.Execute(c.Request.Context(), h.dataset, request.CaseID, c.GetString("userName"), h.auditor, h.observer)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "自主排查初始化失败；未执行修复"})
		return
	}
	score, err := rcascoring.Check(run.Diagnosis)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "诊断完成但评分不可用", "run": run})
		return
	}
	// Truth is consulted only after the Agent stops and never enters its tools.
	c.JSON(http.StatusOK, gin.H{"run": run, "score": score, "evaluation_valid": run.Agent.Completed, "evidence_valid": rcaagent.EvidenceContractSatisfied(run)})
}

// AgentReport exposes the one-pass, real-model evaluation. It never inherits
// scores from the retired deterministic rule comparison.
func (h *Handler) AgentReport(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	f, err := os.Open("evals/rcaeval/agent-evaluation.json")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "自主 Agent 回放报告尚未生成；可直接运行单例查看真实轨迹"})
		return
	}
	defer f.Close()
	var report struct {
		Version              string           `json:"version"`
		DatasetSHA256        string           `json:"dataset_sha256"`
		PromptSHA256         string           `json:"prompt_sha256"`
		ImplementationSHA256 string           `json:"implementation_sha256"`
		Split                string           `json:"split"`
		EvaluationKind       string           `json:"evaluation_kind"`
		GeneratedAt          string           `json:"generated_at"`
		Metrics              map[string]int64 `json:"metrics"`
		Limitations          []string         `json:"limitations"`
		Cases                []struct {
			ID            string           `json:"id"`
			Title         string           `json:"title"`
			Run           rcaagent.Run     `json:"run"`
			Score         rcascoring.Score `json:"score"`
			Valid         bool             `json:"evaluation_valid"`
			EvidenceValid bool             `json:"evidence_valid"`
		} `json:"cases"`
	}
	if json.NewDecoder(io.LimitReader(f, 16<<20)).Decode(&report) != nil || report.Version != rcaagent.Version || report.DatasetSHA256 != h.dataset.SHA256 || report.PromptSHA256 != rcaagent.PromptHash() || report.ImplementationSHA256 != rcaagent.ImplementationHash() || report.Split != "holdout" || report.EvaluationKind != "fixed_known_fault_evaluation" || len(report.Cases) != 6 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "自主 Agent 报告与当前版本不一致，不展示旧成绩"})
		return
	}
	holdout := make(map[string]struct{}, 6)
	for _, item := range h.dataset.Catalog {
		if item.Split == "holdout" {
			holdout[item.ID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(report.Cases))
	recomputedMetrics := map[string]int64{
		"attempted":        int64(len(report.Cases)),
		"completed":        0,
		"execution_failed": 0,
		"service_top1":     0,
		"joint_correct":    0,
		"evidence_valid":   0,
		"model_calls":      0,
		"tool_calls":       0,
		"input_tokens":     0,
		"output_tokens":    0,
		"elapsed_ms_total": 0,
	}
	validReport := len(holdout) == 6
	for _, entry := range report.Cases {
		_, expectedID := holdout[entry.ID]
		_, duplicateID := seen[entry.ID]
		seen[entry.ID] = struct{}{}
		currentScore, scoreErr := rcascoring.Check(entry.Run.Diagnosis)
		evidenceValid := rcaagent.EvidenceContractSatisfied(entry.Run)
		if !expectedID || duplicateID || entry.Run.Diagnosis.CaseID != entry.ID || entry.Score.Answer.ID != entry.ID || entry.Score.Answer.Split != "holdout" ||
			entry.Run.DatasetSHA256 != report.DatasetSHA256 || entry.Run.Agent.Version != report.Version || entry.Run.Agent.PromptSHA256 != report.PromptSHA256 ||
			entry.Run.Agent.ImplementationSHA256 != report.ImplementationSHA256 || scoreErr != nil || currentScore != entry.Score || evidenceValid != entry.EvidenceValid || entry.Valid != entry.Run.Agent.Completed {
			validReport = false
			break
		}
		if entry.Valid {
			recomputedMetrics["completed"]++
			if entry.Score.ServiceTop1 {
				recomputedMetrics["service_top1"]++
			}
			if entry.Score.JointCorrect {
				recomputedMetrics["joint_correct"]++
			}
			if entry.EvidenceValid {
				recomputedMetrics["evidence_valid"]++
			}
		} else {
			recomputedMetrics["execution_failed"]++
		}
		recomputedMetrics["model_calls"] += int64(entry.Run.Diagnosis.ModelCalls)
		recomputedMetrics["tool_calls"] += int64(len(entry.Run.ToolCalls))
		recomputedMetrics["input_tokens"] += int64(entry.Run.Agent.InputTokens)
		recomputedMetrics["output_tokens"] += int64(entry.Run.Agent.OutputTokens)
		recomputedMetrics["elapsed_ms_total"] += int64(entry.Run.ElapsedMS)
	}
	for key, value := range recomputedMetrics {
		if report.Metrics[key] != value {
			validReport = false
		}
	}
	if !validReport || len(seen) != len(holdout) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "自主 Agent 报告完整性校验失败，不展示不可复核成绩"})
		return
	}
	if id := c.Param("id"); id != "" {
		for _, entry := range report.Cases {
			if entry.ID == id {
				c.JSON(http.StatusOK, gin.H{"run": entry.Run, "score": entry.Score, "evaluation_valid": entry.Valid, "evidence_valid": entry.EvidenceValid, "recorded": true, "recorded_at": report.GeneratedAt})
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"message": "本次记录中没有该案例"})
		return
	}
	rows := make([]gin.H, 0, len(report.Cases))
	for _, entry := range report.Cases {
		tools := []string{}
		for _, s := range entry.Run.Agent.Steps {
			if s.Decision != nil && s.Decision.Tool != nil {
				tools = append(tools, s.Decision.Tool.Name+":"+s.Decision.Tool.Service)
			}
		}
		rows = append(rows, gin.H{"id": entry.ID, "title": entry.Title, "score": entry.Score, "valid": entry.Valid, "evidence_valid": entry.EvidenceValid, "stop_reason": entry.Run.Agent.StopReason, "model_calls": entry.Run.Diagnosis.ModelCalls, "tool_calls": len(entry.Run.ToolCalls), "tools": tools, "input_tokens": entry.Run.Agent.InputTokens, "output_tokens": entry.Run.Agent.OutputTokens, "elapsed_ms": entry.Run.ElapsedMS, "model": entry.Run.Agent.Model, "summary": entry.Run.Diagnosis.Summary})
	}
	c.JSON(http.StatusOK, gin.H{"version": report.Version, "dataset_sha256": report.DatasetSHA256, "prompt_sha256": report.PromptSHA256, "implementation_sha256": report.ImplementationSHA256, "split": report.Split, "generated_at": report.GeneratedAt, "evaluation_kind": report.EvaluationKind, "metrics": report.Metrics, "limitations": report.Limitations, "cases": rows})
}
