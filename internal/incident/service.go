package incident

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/model"

	"github.com/google/uuid"
)

type RunReader interface {
	Get(context.Context, string, string) (diagnostic.RunResponse, error)
}

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	runs       RunReader
	repository Repository
	clock      Clock
}

func NewService(runs RunReader, repository Repository, clock Clock) (*Service, error) {
	if runs == nil || repository == nil || clock == nil {
		return nil, errors.New("run reader, incident repository and clock are required")
	}
	return &Service{runs: runs, repository: repository, clock: clock}, nil
}

func (service *Service) Preview(ctx context.Context, userID string, runID string, hypothesisID string) (Proposal, error) {
	response, hypothesis, err := service.eligibleHypothesis(ctx, userID, runID, hypothesisID)
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{
		SchemaVersion: SchemaVersion, ProposalID: proposalID(response.Detail.Run.RunID, hypothesis.ID, response.Detail.Run.StateVersion),
		RunID: response.Detail.Run.RunID, ExpectedStateVersion: response.Detail.Run.StateVersion, HypothesisID: hypothesis.ID,
		Symptom: response.Result.Symptom, ProposedRootCause: hypothesis.Cause,
		Evidence:          append([]diagnostic.EvidenceReference(nil), hypothesis.Evidence...),
		VerificationSteps: append([]diagnostic.VerificationStep(nil), hypothesis.VerificationSteps...), RequiresHumanConfirm: true,
	}, nil
}

func (service *Service) Confirm(ctx context.Context, command ConfirmCommand) (Confirmation, error) {
	command.ClientRequestID = strings.TrimSpace(command.ClientRequestID)
	command.HypothesisID = strings.TrimSpace(command.HypothesisID)
	if command.ClientRequestID == "" || utf8.RuneCountInString(command.ClientRequestID) > 128 || command.ExpectedStateVersion <= 0 {
		return Confirmation{}, ErrInvalidConfirmation
	}
	resolution, _, err := diagnostic.SanitizeFreeText(command.Resolution, 1000)
	if err != nil || utf8.RuneCountInString(resolution) < 5 {
		return Confirmation{}, ErrInvalidConfirmation
	}
	response, hypothesis, err := service.eligibleHypothesis(ctx, command.UserID, command.RunID, command.HypothesisID)
	if err != nil {
		return Confirmation{}, err
	}
	if response.Detail.Run.StateVersion != command.ExpectedStateVersion {
		return Confirmation{}, harness.ErrRunConflict
	}
	now := service.clock.Now()
	feedbackID, incidentID, eventID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	components, _ := json.Marshal(response.Result.Components)
	signatures, _ := json.Marshal(response.Result.ErrorSignatures)
	evidence, _ := json.Marshal(hypothesis.Evidence)
	resolutionHash := sha256Text(resolution)
	feedback := model.ResolutionFeedback{
		ID: feedbackID, TenantIDHash: response.Detail.Run.TenantIDHash, UserIDHash: response.Detail.Run.UserIDHash,
		ClientRequestID: command.ClientRequestID, SourceRunID: response.Detail.Run.RunID, HypothesisID: hypothesis.ID,
		ResolutionSHA256: resolutionHash, FeedbackType: FeedbackConfirmed, CreatedAt: now,
	}
	incident := model.ResolvedIncident{
		ID: incidentID, TenantIDHash: response.Detail.Run.TenantIDHash, UserIDHash: response.Detail.Run.UserIDHash,
		SourceRunID: response.Detail.Run.RunID, FeedbackID: feedbackID, SessionID: response.Detail.Run.SessionID,
		SchemaVersion: SchemaVersion, ExtractorVersion: ExtractorVersion, HypothesisID: hypothesis.ID,
		Symptom: response.Result.Symptom, RootCause: hypothesis.Cause, Resolution: resolution,
		ComponentsJSON: string(components), ErrorSignaturesJSON: string(signatures), EvidenceJSON: string(evidence),
		Status: StatusConfirmed, IndexStatus: IndexStatusPending, Version: 1, ConfirmedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	payload, _ := json.Marshal(map[string]any{"incident_id": incidentID, "version": 1})
	event := model.OutboxEvent{
		ID: eventID, Topic: IndexTopic, EventType: IndexEventType, TraceID: response.Detail.Run.TraceID,
		TenantID: response.Detail.Run.TenantIDHash, AggregateID: incidentID, AggregateVersion: 1,
		PayloadJSON: string(payload), Status: OutboxStatusPending, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	run := model.AgentLifecycleRun{RunID: response.Detail.Run.RunID, TenantIDHash: response.Detail.Run.TenantIDHash, UserIDHash: response.Detail.Run.UserIDHash, State: string(response.Detail.Run.State), StateVersion: response.Detail.Run.StateVersion}
	persisted, created, err := service.repository.Confirm(ctx, confirmationWrite{Run: run, Feedback: feedback, Incident: incident, Event: event, ResolutionSHA256: resolutionHash})
	if err != nil {
		return Confirmation{}, err
	}
	public, err := publicIncident(persisted)
	if err != nil {
		return Confirmation{}, err
	}
	return Confirmation{SchemaVersion: SchemaVersion, Created: created, Incident: public}, nil
}

func (service *Service) Get(ctx context.Context, userID string, runID string) (*PublicResolvedIncident, error) {
	incident, err := service.repository.GetByRun(ctx, harness.PrincipalHash(userID), strings.TrimSpace(runID))
	if err != nil || incident == nil {
		return nil, err
	}
	result, err := publicIncident(*incident)
	return &result, err
}

func (service *Service) eligibleHypothesis(ctx context.Context, userID string, runID string, hypothesisID string) (diagnostic.RunResponse, diagnostic.Hypothesis, error) {
	response, err := service.runs.Get(ctx, strings.TrimSpace(runID), userID)
	if err != nil {
		return diagnostic.RunResponse{}, diagnostic.Hypothesis{}, err
	}
	if response.Detail.Run.State != harness.StateSucceeded || response.Result == nil || response.Result.ConclusionStatus != diagnostic.ConclusionHypothesis {
		return diagnostic.RunResponse{}, diagnostic.Hypothesis{}, ErrRunNotEligible
	}
	hypothesisID = strings.TrimSpace(hypothesisID)
	if hypothesisID == "" && len(response.Result.Hypotheses) > 0 {
		hypothesisID = response.Result.Hypotheses[0].ID
	}
	for _, hypothesis := range response.Result.Hypotheses {
		if hypothesis.ID == hypothesisID {
			return response, hypothesis, nil
		}
	}
	return diagnostic.RunResponse{}, diagnostic.Hypothesis{}, ErrHypothesisNotFound
}

func proposalID(runID string, hypothesisID string, version int64) string {
	return sha256Text(runID + "\x00" + hypothesisID + "\x00" + strconv.FormatInt(version, 10))[:24]
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func publicIncident(value model.ResolvedIncident) (PublicResolvedIncident, error) {
	result := PublicResolvedIncident{
		ID: value.ID, SourceRunID: value.SourceRunID, SessionID: value.SessionID, HypothesisID: value.HypothesisID,
		Symptom: value.Symptom, RootCause: value.RootCause, Resolution: value.Resolution, Status: value.Status,
		IndexStatus: value.IndexStatus, ConfirmedAt: value.ConfirmedAt, IndexedAt: value.IndexedAt,
	}
	if err := json.Unmarshal([]byte(value.ComponentsJSON), &result.Components); err != nil {
		return PublicResolvedIncident{}, err
	}
	if err := json.Unmarshal([]byte(value.ErrorSignaturesJSON), &result.ErrorSignatures); err != nil {
		return PublicResolvedIncident{}, err
	}
	return result, nil
}
