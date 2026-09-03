package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAttachPreservesValidRequestIDAndCreatesTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Attach())
	router.GET("/", func(context *gin.Context) {
		requestID, traceID := IDs(context)
		context.JSON(http.StatusOK, gin.H{"request_id": requestID, "trace_id": traceID})
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "client-request-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Header().Get(RequestIDHeader) != "client-request-1" {
		t.Fatalf("request id was not preserved: %q", response.Header().Get(RequestIDHeader))
	}
	if response.Header().Get(TraceIDHeader) == "" {
		t.Fatal("trace id was not generated")
	}
}

func TestAttachRejectsUnsafeRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Attach())
	router.GET("/", func(context *gin.Context) { context.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "invalid request id\nvalue")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Header().Get(RequestIDHeader) == "invalid request id\nvalue" {
		t.Fatal("unsafe request id was trusted")
	}
}
