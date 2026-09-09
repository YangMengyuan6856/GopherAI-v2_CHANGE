package health

import (
	healthservice "GopherAI/internal/health"
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Service interface {
	Live() healthservice.LiveResponse
	Ready(ctx context.Context) (healthservice.ReadyResponse, bool)
}

type Controller struct {
	service Service
}

func NewController(service Service) *Controller {
	return &Controller{service: service}
}

func (controller *Controller) Live(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, controller.service.Live())
}

func (controller *Controller) Ready(ctx *gin.Context) {
	response, ready := controller.service.Ready(ctx.Request.Context())
	if !ready {
		ctx.JSON(http.StatusServiceUnavailable, response)
		return
	}
	ctx.JSON(http.StatusOK, response)
}
