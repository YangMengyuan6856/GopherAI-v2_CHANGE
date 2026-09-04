package profilememory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/model"
)

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("profile memory repository and clock are required")
	}
	return &Service{repository: repository, clock: clock}, nil
}

func (service *Service) Capture(ctx context.Context, tenantID string, userID string, sourceRunID string, extracted diagnostic.ExtractedInput) error {
	tenantID, userID, sourceRunID = strings.TrimSpace(tenantID), strings.TrimSpace(userID), strings.TrimSpace(sourceRunID)
	if tenantID == "" || userID == "" || sourceRunID == "" {
		return ErrInvalidProfileMemory
	}
	facts := make([]capturedFact, 0, len(extracted.KnownEnvironment))
	for _, fact := range extracted.KnownEnvironment {
		key, value := strings.TrimSpace(fact.Key), strings.TrimSpace(fact.Value)
		if _, allowed := allowedKeys[key]; !allowed || value == "" || utf8.RuneCountInString(value) > 256 || fact.Confidence <= 0 || fact.Confidence > 1 {
			continue
		}
		facts = append(facts, capturedFact{Key: key, Value: value, Confidence: fact.Confidence})
	}
	if len(facts) == 0 {
		return nil
	}
	return service.repository.Capture(ctx, harness.PrincipalHash(tenantID), harness.PrincipalHash(userID), sourceRunID, facts, service.clock.Now())
}

func (service *Service) List(ctx context.Context, userID string) (ListResponse, error) {
	items, err := service.repository.List(ctx, harness.PrincipalHash(userID))
	if err != nil {
		return ListResponse{}, err
	}
	response := ListResponse{SchemaVersion: SchemaVersion, Items: make([]PublicMemory, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, publicMemory(item))
		switch item.Status {
		case StatusActive:
			response.ActiveCount++
		case StatusCandidate:
			response.CandidateCount++
		case StatusConflicted:
			response.ConflictCount++
		}
	}
	return response, nil
}

func (service *Service) Correct(ctx context.Context, command Correction) (PublicMemory, error) {
	value, _, err := diagnostic.SanitizeFreeText(command.Value, 256)
	if err != nil || utf8.RuneCountInString(value) > 256 {
		return PublicMemory{}, ErrInvalidProfileMemory
	}
	now := service.clock.Now()
	expiresAt := now.Add(ConfirmedTTL)
	var expiry *time.Time = &expiresAt
	if command.ExpiresInDays != nil {
		if *command.ExpiresInDays < 0 || *command.ExpiresInDays > 365 {
			return PublicMemory{}, ErrInvalidProfileMemory
		}
		if *command.ExpiresInDays == 0 {
			expiry = nil
		} else {
			value := now.Add(time.Duration(*command.ExpiresInDays) * 24 * time.Hour)
			expiry = &value
		}
	}
	result, err := service.repository.Correct(ctx, harness.PrincipalHash(command.UserID), strings.TrimSpace(command.MemoryID), value, expiry, now)
	if err != nil {
		return PublicMemory{}, err
	}
	return publicMemory(result), nil
}

func (service *Service) Delete(ctx context.Context, userID string, memoryID string) error {
	return service.repository.Delete(ctx, harness.PrincipalHash(userID), strings.TrimSpace(memoryID))
}

func publicMemory(value model.EnvironmentMemory) PublicMemory {
	return PublicMemory{
		ID: value.ID, Key: value.Key, Value: value.Value, SourceType: value.SourceType, SourceRunID: value.SourceRunID,
		Confidence: value.Confidence, Status: value.Status, Version: value.Version, ExpiresAt: value.ExpiresAt,
		LastObservedAt: value.LastObservedAt, UpdatedAt: value.UpdatedAt,
	}
}
