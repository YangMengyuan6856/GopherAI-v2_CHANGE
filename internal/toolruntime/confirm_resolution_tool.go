package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"GopherAI/internal/harness"
	"GopherAI/internal/incident"
)

const (
	ErrorResolutionRunNotEligible = "RUN_NOT_RESOLUTION_ELIGIBLE"
	ErrorResolutionHypothesis     = "HYPOTHESIS_NOT_FOUND"
	ErrorResolutionInvalid        = "RESOLUTION_CONFIRMATION_INVALID"
	ErrorResolutionIdempotency    = "IDEMPOTENCY_KEY_REUSED"
	ErrorResolutionAlreadyExists  = "RESOLUTION_ALREADY_CONFIRMED"
	ErrorResolutionStateConflict  = "RUN_STATE_CONFLICT"
	ErrorResolutionUnavailable    = "RESOLUTION_CONFIRMATION_UNAVAILABLE"
)

type ResolutionConfirmer interface {
	Confirm(context.Context, incident.ConfirmCommand) (incident.Confirmation, error)
}

// ConfirmResolutionTool is deliberately not part of the public ToolAgent
// registry. The diagnostic confirmation endpoint registers it in a dedicated
// runtime and grants internal-write permission only after an explicit user
// confirmation carrying a state version and idempotency key.
type ConfirmResolutionTool struct{ confirmer ResolutionConfirmer }

func NewConfirmResolutionTool(confirmer ResolutionConfirmer) *ConfirmResolutionTool {
	return &ConfirmResolutionTool{confirmer: confirmer}
}

func (tool *ConfirmResolutionTool) Definition() Definition {
	return Definition{
		Name: "confirm_resolution", Version: "1.0.0",
		Description: "Persist an explicitly user-confirmed diagnostic resolution through the idempotent incident transaction and outbox boundary.",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"run_id":                 {Type: "string", MinLength: 1, MaxLength: 128},
			"hypothesis_id":          {Type: "string", MinLength: 1, MaxLength: 128},
			"resolution":             {Type: "string", MinLength: 5, MaxLength: 1000},
			"client_request_id":      {Type: "string", MinLength: 1, MaxLength: 128},
			"expected_state_version": {Type: "integer"},
		}, Required: []string{"run_id", "hypothesis_id", "resolution", "client_request_id", "expected_state_version"}, AdditionalProperties: false},
		AllowedIntents: []string{"troubleshooting"}, RequiredPermission: "devsupport:resolution:confirm",
		SideEffect: SideEffectInternalWrite, TimeoutMS: 5000, MaxResultBytes: 32 * 1024,
		Idempotent: true, RetryMaxAttempts: 2,
	}
}

func (tool *ConfirmResolutionTool) Execute(ctx context.Context, arguments map[string]any) (Output, error) {
	if tool == nil || tool.confirmer == nil {
		return Output{Retryable: true}, newCodedExecutionError(ErrorResolutionUnavailable, true, errors.New("resolution confirmer is unavailable"))
	}
	principal, ok := executionPrincipal(ctx)
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		return Output{}, newCodedExecutionError(ErrorPermissionDenied, false, errors.New("trusted execution principal is missing"))
	}
	version, err := arguments["expected_state_version"].(json.Number).Int64()
	if err != nil {
		return Output{}, newCodedExecutionError(ErrorResolutionInvalid, false, errors.New("state version is invalid"))
	}
	confirmation, err := tool.confirmer.Confirm(ctx, incident.ConfirmCommand{
		RunID: strings.TrimSpace(arguments["run_id"].(string)), UserID: principal.UserID,
		HypothesisID: strings.TrimSpace(arguments["hypothesis_id"].(string)), Resolution: arguments["resolution"].(string),
		ClientRequestID: strings.TrimSpace(arguments["client_request_id"].(string)), ExpectedStateVersion: version,
	})
	if err != nil {
		code, retryable := resolutionErrorCode(err)
		return Output{Retryable: retryable}, newCodedExecutionError(code, retryable, err)
	}
	return Output{Data: confirmation, EvidenceRefs: []string{"resolution-confirmation:" + confirmation.Incident.ID}}, nil
}

func resolutionErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, incident.ErrRunNotEligible):
		return ErrorResolutionRunNotEligible, false
	case errors.Is(err, incident.ErrHypothesisNotFound):
		return ErrorResolutionHypothesis, false
	case errors.Is(err, incident.ErrInvalidConfirmation):
		return ErrorResolutionInvalid, false
	case errors.Is(err, incident.ErrIdempotencyConflict):
		return ErrorResolutionIdempotency, false
	case errors.Is(err, incident.ErrAlreadyConfirmed):
		return ErrorResolutionAlreadyExists, false
	case errors.Is(err, harness.ErrRunConflict):
		return ErrorResolutionStateConflict, false
	default:
		return ErrorResolutionUnavailable, true
	}
}
