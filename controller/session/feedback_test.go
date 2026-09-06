package session

import (
	"GopherAI/internal/feedback"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type feedbackApplicationStub struct {
	userID    string
	requestID string
	input     feedback.Submission
}

func (stub *feedbackApplicationStub) Submit(_ context.Context, userID, requestID string, input feedback.Submission) (feedback.Receipt, error) {
	stub.userID, stub.requestID, stub.input = userID, requestID, input
	return feedback.Receipt{SchemaVersion: feedback.SchemaVersion, FeedbackID: "feedback-1", SampleID: "sample-1", Created: true, Status: "pending", FeedbackType: feedback.FeedbackDownvote, SampleRateBasis: 10000, Queue: "gopher.eval.online.v1", CreatedAt: time.Now()}, nil
}

func TestFeedbackEndpointBindsAuthenticatedOwnerAndReturnsAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := new(feedbackApplicationStub)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Set("userName", "alice")
	ginContext.Params = gin.Params{{Key: "request_id", Value: "request-1"}}
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages/request-1/feedback", bytes.NewBufferString(`{"trace_id":"trace-1","question":"q","answer":"a","confidence":0.5,"resolved":false,"feedback":"user_downvote"}`))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	NewFeedbackHandler(application).Submit(ginContext)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if application.userID != "alice" || application.requestID != "request-1" || application.input.Feedback != feedback.FeedbackDownvote {
		t.Fatalf("authenticated feedback identity not propagated: %+v", application)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"question", "answer", "user_hash", "trace_hash"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("feedback response exposed %s: %s", forbidden, recorder.Body.String())
		}
	}
}
