package session

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/feedback"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maximumFeedbackBodyBytes = 64 << 10

type FeedbackApplication interface {
	Submit(context.Context, string, string, feedback.Submission) (feedback.Receipt, error)
}

type FeedbackHandler struct{ application FeedbackApplication }

type FeedbackRequest struct {
	TraceID    string  `json:"trace_id" binding:"required"`
	Question   string  `json:"question" binding:"required"`
	Answer     string  `json:"answer" binding:"required"`
	Confidence float64 `json:"confidence"`
	Resolved   bool    `json:"resolved"`
	Feedback   string  `json:"feedback" binding:"required"`
}

func NewFeedbackHandler(application FeedbackApplication) *FeedbackHandler {
	return &FeedbackHandler{application: application}
}

func NewDefaultFeedbackHandler() *FeedbackHandler {
	return NewFeedbackHandler(feedback.NewService(feedback.NewGormRepository(mysql.DB), observability.DefaultMetrics(), time.Now))
}

func (handler *FeedbackHandler) Submit(ginContext *gin.Context) {
	if handler == nil || handler.application == nil {
		writeFeedbackError(ginContext, http.StatusServiceUnavailable, "FEEDBACK_UNAVAILABLE", "反馈服务暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maximumFeedbackBodyBytes)
	request := new(FeedbackRequest)
	if err := ginContext.ShouldBindJSON(request); err != nil {
		writeFeedbackError(ginContext, http.StatusBadRequest, "INVALID_FEEDBACK_REQUEST", "反馈参数错误", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	receipt, err := handler.application.Submit(ctx, ginContext.GetString("userName"), strings.TrimSpace(ginContext.Param("request_id")), feedback.Submission{
		TraceID: request.TraceID, Question: request.Question, Answer: request.Answer,
		Confidence: request.Confidence, Resolved: request.Resolved, Feedback: request.Feedback,
	})
	if err != nil {
		switch {
		case errors.Is(err, feedback.ErrInvalidFeedback):
			writeFeedbackError(ginContext, http.StatusBadRequest, "INVALID_FEEDBACK_REQUEST", "反馈参数错误", false)
		case errors.Is(err, feedback.ErrRunNotFound):
			writeFeedbackError(ginContext, http.StatusNotFound, "FEEDBACK_REQUEST_NOT_FOUND", "只能评价当前账号实际产生的回答", false)
		case errors.Is(err, feedback.ErrTraceMismatch):
			writeFeedbackError(ginContext, http.StatusConflict, "FEEDBACK_TRACE_MISMATCH", "回答标识与运行记录不一致", false)
		default:
			writeFeedbackError(ginContext, http.StatusServiceUnavailable, "FEEDBACK_PERSIST_FAILED", "反馈暂未可靠写入，请稍后重试", true)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusAccepted, receipt)
}

func writeFeedbackError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, gin.H{"schema_version": feedback.SchemaVersion, "code": code, "message": message, "retryable": retryable, "trace_id": traceID})
}
