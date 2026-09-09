package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
)

type Transition struct {
	RunID             string
	UserIDHash        string
	ExpectedState     State
	ExpectedVersion   int64
	NextState         State
	Step              PublicStep
	Checkpoint        CheckpointState
	BudgetDelta       BudgetDelta
	TerminalReason    string
	ErrorCode         string
	CommandID         string
	CommandKind       string
	DeadlineExtension time.Duration
	At                time.Time
}

type Repository interface {
	CreateIdempotent(ctx context.Context, run Run, step PublicStep, checkpoint CheckpointState) (Run, bool, error)
	GetOwned(ctx context.Context, runID string, userIDHash string) (Run, error)
	ListSteps(ctx context.Context, runID string) ([]PublicStep, error)
	LatestCheckpoint(ctx context.Context, runID string) (*CheckpointState, error)
	TransitionCAS(ctx context.Context, command Transition) (Run, error)
}

type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) CreateIdempotent(ctx context.Context, run Run, step PublicStep, checkpoint CheckpointState) (Run, bool, error) {
	if repository == nil || repository.db == nil {
		return Run{}, false, gorm.ErrInvalidDB
	}
	var existing model.AgentLifecycleRun
	err := repository.db.WithContext(ctx).
		Where("tenant_id_hash = ? AND user_id_hash = ? AND client_request_id = ?", run.TenantIDHash, run.UserIDHash, run.ClientRequestID).
		First(&existing).Error
	if err == nil {
		return runFromModel(existing), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, false, err
	}

	persisted := runToModel(run)
	persistedStep, err := stepToModel(run.RunID, step)
	if err != nil {
		return Run{}, false, err
	}
	persistedCheckpoint, err := checkpointToModel(run, checkpoint)
	if err != nil {
		return Run{}, false, err
	}
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&persisted).Error; err != nil {
			return err
		}
		if err := tx.Create(&persistedStep).Error; err != nil {
			return err
		}
		return tx.Create(&persistedCheckpoint).Error
	})
	if err == nil {
		return runFromModel(persisted), true, nil
	}
	// A concurrent retry may have won the unique request key. Re-read and return
	// the authoritative run instead of duplicating it.
	if readErr := repository.db.WithContext(ctx).
		Where("tenant_id_hash = ? AND user_id_hash = ? AND client_request_id = ?", run.TenantIDHash, run.UserIDHash, run.ClientRequestID).
		First(&existing).Error; readErr == nil {
		return runFromModel(existing), false, nil
	}
	return Run{}, false, err
}

func (repository *GormRepository) GetOwned(ctx context.Context, runID string, userIDHash string) (Run, error) {
	if repository == nil || repository.db == nil {
		return Run{}, gorm.ErrInvalidDB
	}
	var persisted model.AgentLifecycleRun
	err := repository.db.WithContext(ctx).Where("run_id = ? AND user_id_hash = ?", runID, userIDHash).First(&persisted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runFromModel(persisted), nil
}

func (repository *GormRepository) ListSteps(ctx context.Context, runID string) ([]PublicStep, error) {
	var persisted []model.AgentLifecycleStep
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if err := repository.db.WithContext(ctx).Where("run_id = ?", runID).Order("state_version ASC, id ASC").Find(&persisted).Error; err != nil {
		return nil, err
	}
	steps := make([]PublicStep, 0, len(persisted))
	for _, item := range persisted {
		step, err := stepFromModel(item)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func (repository *GormRepository) LatestCheckpoint(ctx context.Context, runID string) (*CheckpointState, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var persisted model.AgentCheckpoint
	err := repository.db.WithContext(ctx).Where("run_id = ?", runID).Order("state_version DESC").First(&persisted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stateHash(persisted.StateJSON) != persisted.StateHash {
		return nil, fmt.Errorf("checkpoint integrity check failed")
	}
	var state CheckpointState
	if err := json.Unmarshal([]byte(persisted.StateJSON), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (repository *GormRepository) TransitionCAS(ctx context.Context, command Transition) (Run, error) {
	if repository == nil || repository.db == nil {
		return Run{}, gorm.ErrInvalidDB
	}
	if err := ValidateTransition(command.ExpectedState, command.NextState); err != nil {
		return Run{}, err
	}
	var result Run
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.AgentLifecycleRun
		if err := tx.Where("run_id = ? AND user_id_hash = ?", command.RunID, command.UserIDHash).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRunNotFound
			}
			return err
		}
		if State(current.State) != command.ExpectedState || current.StateVersion != command.ExpectedVersion {
			if command.CommandID != "" && current.LastCommandID == command.CommandID && current.LastCommandKind == command.CommandKind {
				result = runFromModel(current)
				return nil
			}
			return ErrRunConflict
		}
		budget := budgetFromModel(current)
		budget.UsedIterations += command.BudgetDelta.Iterations
		budget.UsedToolCalls += command.BudgetDelta.ToolCalls
		budget.UsedInputTokens += command.BudgetDelta.InputTokens
		budget.UsedOutputTokens += command.BudgetDelta.OutputTokens
		budget.UsedCostMicros += command.BudgetDelta.CostMicros
		if err := budget.Validate(); err != nil {
			return err
		}
		nextVersion := current.StateVersion + 1
		command.Step.StateVersion = nextVersion
		if err := command.Step.Validate(); err != nil {
			return err
		}
		at := command.At.UTC()
		if at.IsZero() {
			at = time.Now().UTC()
		}
		updates := map[string]any{
			"state": command.NextState, "state_version": nextVersion, "current_step_id": command.Step.StepID,
			"needs_user_input": command.NextState == StateWaitingUser, "terminal_reason": command.TerminalReason,
			"last_error_code": command.ErrorCode, "used_iterations": budget.UsedIterations, "used_tool_calls": budget.UsedToolCalls,
			"used_input_tokens": budget.UsedInputTokens, "used_output_tokens": budget.UsedOutputTokens, "used_cost_micros": budget.UsedCostMicros,
			"updated_at": at,
		}
		if command.DeadlineExtension > 0 {
			updates["deadline_at"] = current.DeadlineAt.Add(command.DeadlineExtension)
		}
		if IsTerminal(command.NextState) {
			updates["finished_at"] = at
		}
		if command.CommandID != "" {
			updates["last_command_id"] = command.CommandID
			updates["last_command_kind"] = command.CommandKind
		}
		update := tx.Model(&model.AgentLifecycleRun{}).
			Where("run_id = ? AND user_id_hash = ? AND state = ? AND state_version = ?", command.RunID, command.UserIDHash, command.ExpectedState, command.ExpectedVersion).
			Updates(updates)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrRunConflict
		}
		persistedStep, err := stepToModel(command.RunID, command.Step)
		if err != nil {
			return err
		}
		if persistedStep.StartedAt.IsZero() {
			persistedStep.StartedAt = at
		}
		if err := tx.Create(&persistedStep).Error; err != nil {
			return err
		}
		nextRun := runFromModel(current)
		nextRun.State = command.NextState
		nextRun.StateVersion = nextVersion
		nextRun.CurrentStepID = command.Step.StepID
		nextRun.NeedsUserInput = command.NextState == StateWaitingUser
		nextRun.TerminalReason = command.TerminalReason
		nextRun.LastErrorCode = command.ErrorCode
		if command.CommandID != "" {
			nextRun.LastCommandID = command.CommandID
			nextRun.LastCommandKind = command.CommandKind
		}
		nextRun.Budget = budget
		nextRun.UpdatedAt = at
		if command.DeadlineExtension > 0 {
			nextRun.DeadlineAt = current.DeadlineAt.Add(command.DeadlineExtension)
		}
		if IsTerminal(command.NextState) {
			nextRun.FinishedAt = &at
		}
		checkpoint, err := checkpointToModel(nextRun, command.Checkpoint)
		if err != nil {
			return err
		}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return err
		}
		result = nextRun
		return nil
	})
	return result, err
}

func runToModel(run Run) model.AgentLifecycleRun {
	return model.AgentLifecycleRun{
		RunID: run.RunID, TenantIDHash: run.TenantIDHash, UserIDHash: run.UserIDHash, ClientRequestID: run.ClientRequestID,
		RequestID: run.RequestID, TraceID: run.TraceID, SessionID: run.SessionID, Intent: run.Intent, Strategy: run.Strategy,
		PolicyVersion: run.PolicyVersion, HarnessVersion: run.HarnessVersion, ContextVersion: run.ContextVersion,
		State: string(run.State), StateVersion: run.StateVersion, CurrentStepID: run.CurrentStepID, NeedsUserInput: run.NeedsUserInput,
		TerminalReason: run.TerminalReason, LastErrorCode: run.LastErrorCode, LastCommandID: run.LastCommandID, LastCommandKind: run.LastCommandKind,
		MaxIterations: run.Budget.MaxIterations, UsedIterations: run.Budget.UsedIterations,
		MaxToolCalls: run.Budget.MaxToolCalls, UsedToolCalls: run.Budget.UsedToolCalls,
		MaxInputTokens: run.Budget.MaxInputTokens, UsedInputTokens: run.Budget.UsedInputTokens,
		MaxOutputTokens: run.Budget.MaxOutputTokens, UsedOutputTokens: run.Budget.UsedOutputTokens,
		MaxCostMicros: run.Budget.MaxCostMicros, UsedCostMicros: run.Budget.UsedCostMicros,
		StartedAt: run.StartedAt, DeadlineAt: run.DeadlineAt, FinishedAt: run.FinishedAt, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func runFromModel(run model.AgentLifecycleRun) Run {
	return Run{
		RunID: run.RunID, TenantIDHash: run.TenantIDHash, UserIDHash: run.UserIDHash, ClientRequestID: run.ClientRequestID,
		RequestID: run.RequestID, TraceID: run.TraceID, SessionID: run.SessionID, Intent: run.Intent, Strategy: run.Strategy,
		PolicyVersion: run.PolicyVersion, HarnessVersion: run.HarnessVersion, ContextVersion: run.ContextVersion,
		State: State(run.State), StateVersion: run.StateVersion, CurrentStepID: run.CurrentStepID, NeedsUserInput: run.NeedsUserInput,
		TerminalReason: run.TerminalReason, LastErrorCode: run.LastErrorCode, LastCommandID: run.LastCommandID, LastCommandKind: run.LastCommandKind, Budget: budgetFromModel(run),
		StartedAt: run.StartedAt, DeadlineAt: run.DeadlineAt, FinishedAt: run.FinishedAt, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func budgetFromModel(run model.AgentLifecycleRun) Budget {
	return Budget{MaxIterations: run.MaxIterations, UsedIterations: run.UsedIterations, MaxToolCalls: run.MaxToolCalls, UsedToolCalls: run.UsedToolCalls, MaxInputTokens: run.MaxInputTokens, UsedInputTokens: run.UsedInputTokens, MaxOutputTokens: run.MaxOutputTokens, UsedOutputTokens: run.UsedOutputTokens, MaxCostMicros: run.MaxCostMicros, UsedCostMicros: run.UsedCostMicros}
}

func stepToModel(runID string, step PublicStep) (model.AgentLifecycleStep, error) {
	evidence, err := json.Marshal(step.EvidenceRefs)
	if err != nil {
		return model.AgentLifecycleStep{}, err
	}
	tools, err := json.Marshal(step.ToolCallIDs)
	if err != nil {
		return model.AgentLifecycleStep{}, err
	}
	delta, err := json.Marshal(step.BudgetDelta)
	if err != nil {
		return model.AgentLifecycleStep{}, err
	}
	return model.AgentLifecycleStep{RunID: runID, StepID: step.StepID, Attempt: step.Attempt, Kind: step.Kind, Status: step.Status, ReasonCode: step.ReasonCode, PublicSummary: step.PublicSummary, EvidenceRefsJSON: string(evidence), ToolCallIDsJSON: string(tools), BudgetDeltaJSON: string(delta), ErrorCode: step.ErrorCode, StateVersion: step.StateVersion, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt}, nil
}

func stepFromModel(step model.AgentLifecycleStep) (PublicStep, error) {
	result := PublicStep{StepID: step.StepID, Attempt: step.Attempt, Kind: step.Kind, Status: step.Status, ReasonCode: step.ReasonCode, PublicSummary: step.PublicSummary, ErrorCode: step.ErrorCode, StateVersion: step.StateVersion, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt}
	if err := json.Unmarshal([]byte(firstJSON(step.EvidenceRefsJSON, "[]")), &result.EvidenceRefs); err != nil {
		return PublicStep{}, err
	}
	if err := json.Unmarshal([]byte(firstJSON(step.ToolCallIDsJSON, "[]")), &result.ToolCallIDs); err != nil {
		return PublicStep{}, err
	}
	if err := json.Unmarshal([]byte(firstJSON(step.BudgetDeltaJSON, "{}")), &result.BudgetDelta); err != nil {
		return PublicStep{}, err
	}
	return result, nil
}

func checkpointToModel(run Run, state CheckpointState) (model.AgentCheckpoint, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return model.AgentCheckpoint{}, err
	}
	value := string(encoded)
	return model.AgentCheckpoint{RunID: run.RunID, StateVersion: run.StateVersion, SchemaVersion: CheckpointVersion, HarnessVersion: run.HarnessVersion, ContextVersion: run.ContextVersion, StateHash: stateHash(value), StateJSON: value, CreatedAt: run.UpdatedAt}, nil
}

func stateHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func firstJSON(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
