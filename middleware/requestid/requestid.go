package requestid

import (
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	RequestIDHeader = "X-Request-ID"
	TraceIDHeader   = "X-Trace-ID"
	requestIDKey    = "requestID"
	traceIDKey      = "traceID"
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func Attach() gin.HandlerFunc {
	return func(context *gin.Context) {
		requestID := context.GetHeader(RequestIDHeader)
		if !validRequestID.MatchString(requestID) {
			requestID = uuid.NewString()
		}
		traceID := uuid.NewString()
		context.Set(requestIDKey, requestID)
		context.Set(traceIDKey, traceID)
		context.Header(RequestIDHeader, requestID)
		context.Header(TraceIDHeader, traceID)
		context.Next()
	}
}

func IDs(context *gin.Context) (requestID string, traceID string) {
	return context.GetString(requestIDKey), context.GetString(traceIDKey)
}
