package contract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validRequest() RequestContext {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	return RequestContext{
		TraceID: "trace-1", RequestID: "request-1", UserID: "user-1", TenantID: "tenant-1",
		Question: "how do I diagnose redis?", StartedAt: now, Deadline: now.Add(time.Minute),
		Budgets: ExecutionBudgets{MaxAgents: 1, TotalTimeout: time.Minute},
	}
}

func TestRequestContextValidation(t *testing.T) {
	request := validRequest()
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	request.Question = "  "
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "question") {
		t.Fatalf("expected question validation error, got %v", err)
	}
}

func TestTraceEnvelopeSerializationDoesNotExposeInternalCause(t *testing.T) {
	envelope := TraceEnvelope{
		SchemaVersion: SchemaVersion,
		TraceID:       "trace-1",
		RequestID:     "request-1",
		Error:         NewDomainError("MODEL_ERROR", ErrorModel, "模型暂时不可用", true, errSensitive("secret stack")),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret stack") {
		t.Fatalf("serialized contract leaked internal cause: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"schema_version":"1"`) {
		t.Fatalf("schema version missing: %s", encoded)
	}
}

func TestWithTraceClassifiesTimeout(t *testing.T) {
	domainError := WithTrace(context.DeadlineExceeded, "trace-1")
	if domainError.Code != "REQUEST_TIMEOUT" || domainError.Category != ErrorDependencyTimeout || domainError.TraceID != "trace-1" {
		t.Fatalf("unexpected timeout mapping: %#v", domainError)
	}
}

type errSensitive string

func (err errSensitive) Error() string { return string(err) }
