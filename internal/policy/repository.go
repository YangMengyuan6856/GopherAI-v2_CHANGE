package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"GopherAI/model"

	rediscli "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PolicyStatusDraft        = "draft"
	PolicyStatusShadow       = "shadow"
	PolicyStatusCanary       = "canary"
	PolicyStatusActive       = "active"
	PolicyStatusRolledBack   = "rolled_back"
	DefaultPolicyEnvironment = "staging"
	defaultPolicyCacheTTL    = 30 * time.Second
	maximumPolicyBytes       = 128 * 1024
)

var (
	ErrPolicyCacheMiss  = errors.New("routing policy cache miss")
	policyEnvironmentID = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
)

type PolicyAuthority interface {
	Active(context.Context, string) (model.RoutingPolicy, error)
	EnsureActive(context.Context, string, RoutingPolicyDocument, string) (model.RoutingPolicy, error)
}

type ActivePolicyCache interface {
	Load(context.Context, string) (model.RoutingPolicy, error)
	Store(context.Context, string, model.RoutingPolicy) error
	Delete(context.Context, string) error
}

type LoadedPolicy struct {
	Record        model.RoutingPolicy
	Document      RoutingPolicyDocument
	Source        string
	CacheDegraded bool
}

type GormPolicyAuthority struct{ database *gorm.DB }

func NewGormPolicyAuthority(database *gorm.DB) *GormPolicyAuthority {
	return &GormPolicyAuthority{database: database}
}

func (authority *GormPolicyAuthority) Active(ctx context.Context, environment string) (model.RoutingPolicy, error) {
	if authority == nil || authority.database == nil {
		return model.RoutingPolicy{}, errors.New("policy authority database is unavailable")
	}
	if err := validatePolicyEnvironment(environment); err != nil {
		return model.RoutingPolicy{}, err
	}
	var record model.RoutingPolicy
	activeSlot := environment + ":active"
	err := authority.database.WithContext(ctx).
		Where("environment = ? AND status = ? AND active_slot = ?", environment, PolicyStatusActive, activeSlot).
		Order("id DESC").First(&record).Error
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	if _, err := DecodePolicyRecord(record); err != nil {
		return model.RoutingPolicy{}, fmt.Errorf("active policy integrity check failed: %w", err)
	}
	return record, nil
}

func (authority *GormPolicyAuthority) EnsureActive(ctx context.Context, environment string, document RoutingPolicyDocument, createdBy string) (model.RoutingPolicy, error) {
	if authority == nil || authority.database == nil {
		return model.RoutingPolicy{}, errors.New("policy authority database is unavailable")
	}
	if err := validatePolicyEnvironment(environment); err != nil {
		return model.RoutingPolicy{}, err
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" || len(createdBy) > 32 {
		return model.RoutingPolicy{}, errors.New("policy creator is invalid")
	}
	if err := document.Validate(DefaultStrategyRegistry()); err != nil {
		return model.RoutingPolicy{}, fmt.Errorf("policy document is invalid: %w", err)
	}
	encoded, digest, err := encodePolicyDocument(document)
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	activeSlot := environment + ":active"
	now := time.Now().UTC()
	var record model.RoutingPolicy
	err = authority.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		queryErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("environment = ? AND status = ? AND active_slot = ?", environment, PolicyStatusActive, activeSlot).
			Order("id DESC").First(&record).Error
		if queryErr == nil {
			return nil
		}
		if !errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return queryErr
		}
		record = model.RoutingPolicy{
			Version: document.Version, Environment: environment, Status: PolicyStatusActive,
			PolicyJSON: string(encoded), PolicyHash: digest, Reason: "bootstrap_default_policy",
			EvidenceSnapshotJSON: `{}`, CreatedBy: createdBy, ActiveSlot: &activeSlot, ActivatedAt: &now,
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		// A concurrent bootstrap can win the unique active slot. Re-read the
		// MySQL authority rather than guessing from a vendor-specific error.
		if concurrent, readErr := authority.Active(ctx, environment); readErr == nil {
			return concurrent, nil
		}
		return model.RoutingPolicy{}, err
	}
	if _, err := DecodePolicyRecord(record); err != nil {
		return model.RoutingPolicy{}, err
	}
	return record, nil
}

type RedisPolicyCache struct {
	client rediscli.UniversalClient
	ttl    time.Duration
}

func NewRedisPolicyCache(client rediscli.UniversalClient, ttl time.Duration) *RedisPolicyCache {
	if ttl <= 0 {
		ttl = defaultPolicyCacheTTL
	}
	return &RedisPolicyCache{client: client, ttl: ttl}
}

type cachedPolicy struct {
	SchemaVersion string              `json:"schema_version"`
	Record        model.RoutingPolicy `json:"record"`
	PolicyJSON    string              `json:"policy_json"`
}

func (cache *RedisPolicyCache) Load(ctx context.Context, environment string) (model.RoutingPolicy, error) {
	if cache == nil || cache.client == nil {
		return model.RoutingPolicy{}, errors.New("policy cache is unavailable")
	}
	key, err := policyCacheKey(environment)
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	encoded, err := cache.client.Get(ctx, key).Bytes()
	if errors.Is(err, rediscli.Nil) {
		return model.RoutingPolicy{}, ErrPolicyCacheMiss
	}
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	if len(encoded) == 0 || len(encoded) > maximumPolicyBytes {
		return model.RoutingPolicy{}, errors.New("cached policy size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var envelope cachedPolicy
	if err := decoder.Decode(&envelope); err != nil {
		return model.RoutingPolicy{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil || envelope.SchemaVersion != RoutingPolicySchemaVersion {
		return model.RoutingPolicy{}, errors.New("cached policy envelope is invalid")
	}
	envelope.Record.PolicyJSON = envelope.PolicyJSON
	return envelope.Record, nil
}

func (cache *RedisPolicyCache) Store(ctx context.Context, environment string, record model.RoutingPolicy) error {
	if cache == nil || cache.client == nil {
		return errors.New("policy cache is unavailable")
	}
	key, err := policyCacheKey(environment)
	if err != nil {
		return err
	}
	if _, err := DecodePolicyRecord(record); err != nil {
		return err
	}
	envelope := cachedPolicy{SchemaVersion: RoutingPolicySchemaVersion, Record: record, PolicyJSON: record.PolicyJSON}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > maximumPolicyBytes {
		return errors.New("policy cache payload is invalid")
	}
	return cache.client.Set(ctx, key, encoded, cache.ttl).Err()
}

func (cache *RedisPolicyCache) Delete(ctx context.Context, environment string) error {
	if cache == nil || cache.client == nil {
		return errors.New("policy cache is unavailable")
	}
	key, err := policyCacheKey(environment)
	if err != nil {
		return err
	}
	return cache.client.Del(ctx, key).Err()
}

type CachedPolicyRepository struct {
	authority PolicyAuthority
	cache     ActivePolicyCache
}

func NewCachedPolicyRepository(authority PolicyAuthority, cache ActivePolicyCache) *CachedPolicyRepository {
	return &CachedPolicyRepository{authority: authority, cache: cache}
}

func (repository *CachedPolicyRepository) LoadOrSeed(ctx context.Context, environment string, seed RoutingPolicyDocument) (LoadedPolicy, error) {
	if repository == nil || repository.authority == nil {
		return LoadedPolicy{}, errors.New("policy repository is unavailable")
	}
	cacheDegraded := false
	if repository.cache != nil {
		record, err := repository.cache.Load(ctx, environment)
		if err == nil {
			document, decodeErr := DecodePolicyRecord(record)
			if decodeErr == nil && record.Environment == environment && record.Status == PolicyStatusActive {
				return LoadedPolicy{Record: record, Document: document, Source: "redis"}, nil
			}
			cacheDegraded = true
			_ = repository.cache.Delete(ctx, environment)
		} else if !errors.Is(err, ErrPolicyCacheMiss) {
			cacheDegraded = true
		}
	}
	record, err := repository.authority.Active(ctx, environment)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record, err = repository.authority.EnsureActive(ctx, environment, seed, "system_bootstrap")
	}
	if err != nil {
		return LoadedPolicy{}, err
	}
	document, err := DecodePolicyRecord(record)
	if err != nil {
		return LoadedPolicy{}, err
	}
	if repository.cache != nil {
		if cacheErr := repository.cache.Store(ctx, environment, record); cacheErr != nil {
			cacheDegraded = true
		}
	}
	return LoadedPolicy{Record: record, Document: document, Source: "mysql", CacheDegraded: cacheDegraded}, nil
}

func DecodePolicyRecord(record model.RoutingPolicy) (RoutingPolicyDocument, error) {
	if len(record.PolicyJSON) == 0 || len(record.PolicyJSON) > maximumPolicyBytes || len(record.PolicyHash) != sha256.Size*2 {
		return RoutingPolicyDocument{}, errors.New("policy record identity is invalid")
	}
	digest := sha256.Sum256([]byte(record.PolicyJSON))
	if !strings.EqualFold(hex.EncodeToString(digest[:]), record.PolicyHash) {
		return RoutingPolicyDocument{}, errors.New("policy record hash mismatch")
	}
	decoder := json.NewDecoder(strings.NewReader(record.PolicyJSON))
	decoder.DisallowUnknownFields()
	var document RoutingPolicyDocument
	if err := decoder.Decode(&document); err != nil {
		return RoutingPolicyDocument{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RoutingPolicyDocument{}, err
	}
	if document.Version != record.Version || document.SchemaVersion != RoutingPolicySchemaVersion {
		return RoutingPolicyDocument{}, errors.New("policy record version mismatch")
	}
	return document, nil
}

func encodePolicyDocument(document RoutingPolicyDocument) ([]byte, string, error) {
	encoded, err := json.Marshal(document)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumPolicyBytes {
		return nil, "", errors.New("policy document cannot be encoded")
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func policyCacheKey(environment string) (string, error) {
	if err := validatePolicyEnvironment(environment); err != nil {
		return "", err
	}
	return "gopherai:" + environment + ":routing-policy:v1:active", nil
}

func validatePolicyEnvironment(environment string) error {
	if !policyEnvironmentID.MatchString(environment) {
		return errors.New("policy environment is invalid")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("policy JSON has trailing content")
	}
	return nil
}
