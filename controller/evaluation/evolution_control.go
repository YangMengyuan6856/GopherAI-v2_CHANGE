package evaluation

import (
	"context"
	"net/http"
	"time"

	"GopherAI/internal/evolution"

	"github.com/gin-gonic/gin"
)

const defaultEvolutionControlAcceptancePath = "/root/GopherAI_Runtime/evaluation/harness-control-acceptance-latest.json"

type EvolutionControlRunner func(context.Context, time.Time) (evolution.ControlAcceptanceReport, error)

type EvolutionControlHandler struct {
	run   EvolutionControlRunner
	store evolution.ControlAcceptanceStore
}

func NewEvolutionControlHandler(run EvolutionControlRunner, store evolution.ControlAcceptanceStore) *EvolutionControlHandler {
	return &EvolutionControlHandler{run: run, store: store}
}

func NewDefaultEvolutionControlHandler() *EvolutionControlHandler {
	return NewEvolutionControlHandler(evolution.RunControlAcceptance, evolution.NewFileControlAcceptanceStore(defaultEvolutionControlAcceptancePath))
}

func (handler *EvolutionControlHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.store == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_CONTROL_ACCEPTANCE_UNAVAILABLE", "Harness 控制状态机验收暂不可用", true)
		return
	}
	report, err := handler.store.Load()
	if err != nil {
		writeEvolutionError(ginContext, http.StatusNotFound, "HARNESS_CONTROL_ACCEPTANCE_NOT_READY", "请先运行 Harness 控制状态机验收", false)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func (handler *EvolutionControlHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.run == nil || handler.store == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_CONTROL_ACCEPTANCE_UNAVAILABLE", "Harness 控制状态机验收暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	report, err := handler.run(ctx, time.Now())
	if err != nil {
		writeEvolutionError(ginContext, http.StatusUnprocessableEntity, "HARNESS_CONTROL_ACCEPTANCE_FAILED", "Harness 控制状态机验收失败", false)
		return
	}
	if err := handler.store.Save(report); err != nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_CONTROL_ACCEPTANCE_PERSIST_FAILED", "Harness 控制状态机验收报告无法安全落盘", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}
