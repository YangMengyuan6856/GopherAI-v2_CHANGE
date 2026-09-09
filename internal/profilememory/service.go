package profilememory

import (
	"context"
	"errors"
	"sort"
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
	selector   *Selector
}

func NewService(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("profile memory repository and clock are required")
	}
	return &Service{repository: repository, clock: clock, selector: NewSelector()}, nil
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

func (service *Service) Recall(ctx context.Context, tenantID string, userID string, query string, limit int) (RecallResponse, error) {
	response := RecallResponse{SchemaVersion: SchemaVersion, PolicyVersion: RecallPolicyVersion, Status: "no_match", Items: []PublicMemory{}}
	tenantID, userID, query = strings.TrimSpace(tenantID), strings.TrimSpace(userID), strings.TrimSpace(query)
	if tenantID == "" || userID == "" || query == "" {
		return response, ErrInvalidProfileMemory
	}
	if limit <= 0 || limit > MaxRecallResults {
		limit = MaxRecallResults
	}
	now := service.clock.Now()
	items, err := service.repository.RecallActive(ctx, harness.PrincipalHash(tenantID), harness.PrincipalHash(userID), now)
	if err != nil {
		return response, err
	}
	selected := service.selector.Select(harness.PrincipalHash(tenantID), harness.PrincipalHash(userID), query, limit, now, items)
	for _, item := range selected {
		response.Items = append(response.Items, publicMemory(item))
	}
	if len(response.Items) > 0 {
		response.Status = "hit"
	}
	return response, nil
}

type Selector struct{}

func NewSelector() *Selector { return &Selector{} }

func (*Selector) Select(tenantHash string, userHash string, query string, limit int, now time.Time, items []model.EnvironmentMemory) []model.EnvironmentMemory {
	if limit <= 0 || limit > MaxRecallResults {
		limit = MaxRecallResults
	}
	type rankedMemory struct {
		memory model.EnvironmentMemory
		score  int
	}
	ranked := make([]rankedMemory, 0, len(items))
	for _, item := range items {
		_, allowed := allowedKeys[item.Key]
		if !allowed || item.TenantIDHash != tenantHash || item.UserIDHash != userHash || item.Status != StatusActive || item.Confidence < MinRecallConfidence || strings.TrimSpace(item.Value) == "" || (item.ExpiresAt != nil && !item.ExpiresAt.After(now)) {
			continue
		}
		score := profileQueryRelevance(item.Key, query)
		if score > 0 {
			ranked = append(ranked, rankedMemory{memory: item, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if !ranked[i].memory.LastObservedAt.Equal(ranked[j].memory.LastObservedAt) {
			return ranked[i].memory.LastObservedAt.After(ranked[j].memory.LastObservedAt)
		}
		if ranked[i].memory.Key != ranked[j].memory.Key {
			return ranked[i].memory.Key < ranked[j].memory.Key
		}
		return ranked[i].memory.ID < ranked[j].memory.ID
	})
	selected := make([]model.EnvironmentMemory, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range ranked {
		if _, duplicate := seen[item.memory.Key]; duplicate {
			continue
		}
		seen[item.memory.Key] = struct{}{}
		selected = append(selected, item.memory)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func profileQueryRelevance(key string, query string) int {
	query = strings.ToLower(query)
	if containsAny(query, "环境", "environment", "运行配置", "部署信息") {
		return 1
	}
	keywords := map[string][]string{
		"redis_version":   {"redis", "noauth"},
		"mysql_version":   {"mysql", "数据库版本", "database version"},
		"go_version":      {"golang", "go 版本", "go版本", " go ", "go1."},
		"deployment_mode": {"docker", "container", "容器", "kubernetes", "k8s", "部署方式"},
		"cloud_provider":  {"阿里云", "aliyun", "aws", "azure", "gcp", "云厂商", "ecs"},
		"os":              {"ubuntu", "linux", "操作系统", " os "},
	}
	if containsAny(query, keywords[key]...) {
		return 2
	}
	return 0
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
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
