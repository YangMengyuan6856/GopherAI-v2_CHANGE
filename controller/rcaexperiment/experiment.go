package rcaexperimentcontroller

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"GopherAI/common/mysql"
	"GopherAI/internal/observability"
	"GopherAI/internal/rcaexperiment"
	"GopherAI/internal/rcascoring"
	"GopherAI/internal/toolruntime"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	dataset  *rcaexperiment.Dataset
	gate     chan struct{}
	auditor  toolruntime.Auditor
	observer toolruntime.Observer
}

func NewHandler(d *rcaexperiment.Dataset, a toolruntime.Auditor, o toolruntime.Observer) *Handler {
	return &Handler{dataset: d, gate: make(chan struct{}, 1), auditor: a, observer: o}
}
func NewDefaultHandler() *Handler {
	d, err := rcaexperiment.Load()
	if err != nil {
		return NewHandler(nil, nil, nil)
	}
	return NewHandler(d, toolruntime.NewGormAuditor(mysql.DB), observability.DefaultMetrics())
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
	c.JSON(http.StatusOK, gin.H{"version": h.dataset.Version, "revision": h.dataset.Revision, "dataset_sha256": h.dataset.SHA256, "matcher_version": rcaexperiment.MatcherVersion, "cases": h.dataset.Catalog, "references": h.dataset.References, "supported_services": []string{"checkoutservice", "currencyservice"}, "supported_faults": []string{"cpu", "mem", "delay"}, "extractor": h.dataset.Extractor, "model_calls": 0, "max_tool_calls": 4, "max_concurrency": 1})
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
	if request.Strategy != "legacy" && request.Strategy != "feature_only" && request.Strategy != "case_based" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "不支持的实验策略"})
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
	run, err := rcaexperiment.Execute(c.Request.Context(), h.dataset, request.CaseID, request.Strategy, c.GetString("userName"), h.auditor, h.observer)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "只读诊断未完成，未执行任何修复", "tool_calls": run.ToolCalls})
		return
	}
	// The scorer is invoked only AFTER diagnosis and cannot influence matching.
	score, err := rcascoring.Check(run.Diagnosis)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "诊断完成但评分不可用", "run": run})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": run, "score": score})
}
func (h *Handler) Report(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	f, err := os.Open("evals/rcaeval/holdout.json")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "留出报告尚未生成"})
		return
	}
	defer f.Close()
	var r rcascoring.Report
	if err = json.NewDecoder(io.LimitReader(f, 32<<20)).Decode(&r); err != nil || r.DatasetSHA256 != h.dataset.SHA256 || r.PolicySHA256 != h.dataset.PolicySHA256 || r.MatcherVersion != rcaexperiment.MatcherVersion || r.Split != "holdout" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "报告版本与当前实验不一致"})
		return
	}
	rows := make([]gin.H, 0, len(r.Cases))
	for _, row := range r.Cases {
		rows = append(rows, gin.H{"id": row.ID, "strategy": row.Strategy, "score": row.Score, "status": row.Run.Diagnosis.Status, "candidates": row.Run.Diagnosis.Candidates, "error": row.Error})
	}
	c.JSON(http.StatusOK, gin.H{"version": r.Version, "matcher_version": r.MatcherVersion, "dataset_sha256": r.DatasetSHA256, "generated_at": r.GeneratedAt, "metrics": r.Metrics, "cases": rows, "limitations": r.Limitations, "execution_environment": "local_offline"})
}
