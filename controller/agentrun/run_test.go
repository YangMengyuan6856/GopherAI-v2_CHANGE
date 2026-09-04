package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/internal/incident"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type fakeWorkflow struct {
	response      diagnostic.RunResponse
	err           error
	startCommand  diagnostic.StartCommand
	resumeCommand diagnostic.ResumeCommand
	getRunID      string
	cancelRunID   string
}

type fakeResolutionService struct {
	proposal       incident.Proposal
	confirmation   incident.Confirmation
	resolved       *incident.PublicResolvedIncident
	err            error
	confirmCommand incident.ConfirmCommand
	previewUser    string
}

func (service *fakeResolutionService) Preview(_ context.Context, userID string, runID string, hypothesisID string) (incident.Proposal, error) {
	service.previewUser = userID
	if service.proposal.RunID == "" {
		service.proposal = incident.Proposal{SchemaVersion: incident.SchemaVersion, RunID: runID, HypothesisID: hypothesisID, RequiresHumanConfirm: true}
	}
	return service.proposal, service.err
}

func (service *fakeResolutionService) Confirm(_ context.Context, command incident.ConfirmCommand) (incident.Confirmation, error) {
	service.confirmCommand = command
	return service.confirmation, service.err
}

func (service *fakeResolutionService) Get(context.Context, string, string) (*incident.PublicResolvedIncident, error) {
	return service.resolved, service.err
}

func (workflow *fakeWorkflow) Start(_ context.Context, command diagnostic.StartCommand) (diagnostic.RunResponse, error) {
	workflow.startCommand = command
	return workflow.response, workflow.err
}

func (workflow *fakeWorkflow) Get(_ context.Context, runID string, _ string) (diagnostic.RunResponse, error) {
	workflow.getRunID = runID
	return workflow.response, workflow.err
}

func (workflow *fakeWorkflow) Resume(_ context.Context, command diagnostic.ResumeCommand) (diagnostic.RunResponse, error) {
	workflow.resumeCommand = command
	return workflow.response, workflow.err
}

func (workflow *fakeWorkflow) Cancel(_ context.Context, runID string, _ string) (diagnostic.RunResponse, error) {
	workflow.cancelRunID = runID
	return workflow.response, workflow.err
}

func testRunResponse() diagnostic.RunResponse {
	now := time.Date(2026, 9, 5, 1, 30, 0, 0, time.UTC)
	return diagnostic.RunResponse{
		Created: true,
		Detail: harness.RunDetail{
			Run: harness.Run{
				RunID: "run-public", TenantIDHash: "must-not-leak", UserIDHash: "must-not-leak",
				ClientRequestID: "client-1", RequestID: "request-1", TraceID: "trace-public",
				Intent: diagnostic.IntentName, Strategy: diagnostic.StrategyName, PolicyVersion: diagnostic.PolicyVersion,
				HarnessVersion: harness.Version, ContextVersion: harness.ContextVersion, State: harness.StateWaitingUser,
				StateVersion: 5, Budget: harness.DefaultBudget(), StartedAt: now, DeadlineAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
			},
			Steps: []harness.PublicStep{{StepID: "public-step", Attempt: 1, Kind: "context", PublicSummary: "safe summary", StateVersion: 1, StartedAt: now}},
			Checkpoint: &harness.CheckpointState{
				Goal: "diagnose", OpenQuestions: []string{"which component?"}, NextAction: "await_user",
				ArtifactType: "private", Artifact: json.RawMessage(`{"secret":"must-not-leak"}`),
			},
		},
	}
}

func newTestEngine(workflow Workflow) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "alice")
		context.Next()
	})
	handler := NewHandler(workflow)
	engine.POST("/agent-runs/diagnostics", handler.Start)
	engine.GET("/agent-runs/:run_id", handler.Get)
	engine.POST("/agent-runs/:run_id/resume", handler.Resume)
	engine.POST("/agent-runs/:run_id/cancel", handler.Cancel)
	return engine
}

func newResolutionTestEngine(workflow Workflow, resolutions ResolutionService) *gin.Engine {
	gine := newTestEngine(workflow)
	handler := NewHandlerWithResolutions(workflow, resolutions)
	gine.POST("/agent-runs/:run_id/resolution-proposals", handler.PreviewResolution)
	gine.POST("/agent-runs/:run_id/resolution-confirmations", handler.ConfirmResolution)
	gine.GET("/agent-runs/:run_id/resolution", handler.GetResolution)
	return gine
}

func TestStartContractUsesRequestContextAndHidesPrivateCheckpoint(t *testing.T) {
	workflow := &fakeWorkflow{response: testRunResponse()}
	request := httptest.NewRequest(http.MethodPost, "/agent-runs/diagnostics", bytes.NewBufferString(`{"message":"redis noauth"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(requestid.RequestIDHeader, "caller-request-1")
	recorder := httptest.NewRecorder()
	newTestEngine(workflow).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if workflow.startCommand.UserID != "alice" || workflow.startCommand.TenantID != "alice" || workflow.startCommand.ClientRequestID != "caller-request-1" || workflow.startCommand.RequestID != "caller-request-1" || workflow.startCommand.TraceID == "" {
		t.Fatalf("request context was not propagated: %+v", workflow.startCommand)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"must-not-leak", "tenant_id_hash", "user_id_hash", "artifact_type", "artifact"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private lifecycle data leaked in response: %s", body)
		}
	}
	var response Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != responseSchemaVersion || response.Run.RunID != "run-public" || response.Checkpoint == nil || len(response.Checkpoint.OpenQuestions) != 1 {
		t.Fatalf("unexpected public response: %+v", response)
	}
}

func TestResumeConflictUsesStableErrorContract(t *testing.T) {
	workflow := &fakeWorkflow{err: harness.ErrRunConflict}
	request := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/resume", bytes.NewBufferString(`{"message":"more logs","client_request_id":"resume-1","expected_state_version":5}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	newTestEngine(workflow).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != responseSchemaVersion || response.Code != "RUN_STATE_CONFLICT" || !response.Retryable || response.TraceID == "" {
		t.Fatalf("unexpected conflict contract: %+v", response)
	}
}

func TestGetHidesOwnershipFailureAsNotFound(t *testing.T) {
	workflow := &fakeWorkflow{err: harness.ErrRunNotFound}
	request := httptest.NewRequest(http.MethodGet, "/agent-runs/another-users-run", nil)
	recorder := httptest.NewRecorder()
	newTestEngine(workflow).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound || workflow.getRunID != "another-users-run" {
		t.Fatalf("unexpected ownership response %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"AGENT_RUN_NOT_FOUND"`) {
		t.Fatalf("stable not-found code missing: %s", recorder.Body.String())
	}
}

func TestInvalidResumeBodyDoesNotReachWorkflow(t *testing.T) {
	workflow := &fakeWorkflow{err: errors.New("workflow should not be called")}
	request := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/resume", bytes.NewBufferString(`{"message":"missing version"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	newTestEngine(workflow).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || workflow.resumeCommand.RunID != "" {
		t.Fatalf("invalid input reached workflow or wrong response: %d %+v", recorder.Code, workflow.resumeCommand)
	}
}

func TestResolutionPreviewAndConfirmationContracts(t *testing.T) {
	workflow := &fakeWorkflow{response: testRunResponse()}
	resolutions := &fakeResolutionService{confirmation: incident.Confirmation{
		SchemaVersion: incident.SchemaVersion, Created: true,
		Incident: incident.PublicResolvedIncident{ID: "incident-1", SourceRunID: "run-1", Status: incident.StatusConfirmed, IndexStatus: incident.IndexStatusPending},
	}}
	engine := newResolutionTestEngine(workflow, resolutions)

	preview := httptest.NewRecorder()
	previewRequest := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/resolution-proposals", bytes.NewBufferString(`{"hypothesis_id":"cause-1"}`))
	previewRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(preview, previewRequest)
	if preview.Code != http.StatusOK || resolutions.previewUser != "alice" || !strings.Contains(preview.Body.String(), `"requires_human_confirm":true`) {
		t.Fatalf("unexpected preview contract %d: %s", preview.Code, preview.Body.String())
	}

	confirmation := httptest.NewRecorder()
	confirmRequest := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/resolution-confirmations", bytes.NewBufferString(`{"hypothesis_id":"cause-1","resolution":"fixed and verified","client_request_id":"confirm-1","expected_state_version":5}`))
	confirmRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(confirmation, confirmRequest)
	if confirmation.Code != http.StatusCreated || resolutions.confirmCommand.UserID != "alice" || resolutions.confirmCommand.ExpectedStateVersion != 5 {
		t.Fatalf("unexpected confirmation contract %d: %s command=%+v", confirmation.Code, confirmation.Body.String(), resolutions.confirmCommand)
	}
	for _, forbidden := range []string{"tenant_id_hash", "user_id_hash"} {
		if strings.Contains(confirmation.Body.String(), forbidden) {
			t.Fatalf("principal hash leaked: %s", confirmation.Body.String())
		}
	}
}
