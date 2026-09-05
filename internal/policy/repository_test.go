package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"GopherAI/model"

	"gorm.io/gorm"
)

type fakePolicyAuthority struct {
	record      model.RoutingPolicy
	activeErr   error
	activeCalls int
	ensureCalls int
}

func (authority *fakePolicyAuthority) Active(context.Context, string) (model.RoutingPolicy, error) {
	authority.activeCalls++
	return authority.record, authority.activeErr
}

func (authority *fakePolicyAuthority) EnsureActive(_ context.Context, environment string, document RoutingPolicyDocument, _ string) (model.RoutingPolicy, error) {
	authority.ensureCalls++
	record, err := testPolicyRecord(environment, document)
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	authority.record = record
	authority.activeErr = nil
	return record, nil
}

type fakePolicyCache struct {
	record      model.RoutingPolicy
	loadErr     error
	storeErr    error
	storeCalls  int
	deleteCalls int
}

func (cache *fakePolicyCache) Load(context.Context, string) (model.RoutingPolicy, error) {
	return cache.record, cache.loadErr
}

func (cache *fakePolicyCache) Store(_ context.Context, _ string, record model.RoutingPolicy) error {
	cache.storeCalls++
	cache.record = record
	return cache.storeErr
}

func (cache *fakePolicyCache) Delete(context.Context, string) error {
	cache.deleteCalls++
	return nil
}

func testPolicyRecord(environment string, document RoutingPolicyDocument) (model.RoutingPolicy, error) {
	encoded, digest, err := encodePolicyDocument(document)
	if err != nil {
		return model.RoutingPolicy{}, err
	}
	return model.RoutingPolicy{ID: 1, Version: document.Version, Environment: environment, Status: PolicyStatusActive, PolicyJSON: string(encoded), PolicyHash: digest}, nil
}

func TestCachedPolicyRepositorySeedsMySQLThenWarmsRedis(t *testing.T) {
	authority := &fakePolicyAuthority{activeErr: gorm.ErrRecordNotFound}
	cache := &fakePolicyCache{loadErr: ErrPolicyCacheMiss}
	repository := NewCachedPolicyRepository(authority, cache)
	loaded, err := repository.LoadOrSeed(context.Background(), DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "mysql" || loaded.CacheDegraded || authority.ensureCalls != 1 || cache.storeCalls != 1 || loaded.Document.Version != DefaultRoutingPolicy().Version {
		t.Fatalf("unexpected bootstrap result: loaded=%+v authority=%+v cache=%+v", loaded, authority, cache)
	}
}

func TestCachedPolicyRepositoryUsesValidRedisWithoutMySQL(t *testing.T) {
	record, err := testPolicyRecord(DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakePolicyAuthority{activeErr: errors.New("mysql must not be called")}
	cache := &fakePolicyCache{record: record}
	loaded, err := NewCachedPolicyRepository(authority, cache).LoadOrSeed(context.Background(), DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "redis" || authority.activeCalls != 0 {
		t.Fatalf("cache hit unexpectedly called MySQL: loaded=%+v calls=%d", loaded, authority.activeCalls)
	}
}

func TestCachedPolicyRepositoryRejectsCorruptCacheAndFallsBack(t *testing.T) {
	record, err := testPolicyRecord(DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	corrupt := record
	corrupt.PolicyJSON += " "
	cache := &fakePolicyCache{record: corrupt}
	authority := &fakePolicyAuthority{record: record}
	loaded, err := NewCachedPolicyRepository(authority, cache).LoadOrSeed(context.Background(), DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "mysql" || !loaded.CacheDegraded || cache.deleteCalls != 1 || authority.activeCalls != 1 {
		t.Fatalf("corrupt cache was not repaired: loaded=%+v cache=%+v authority=%+v", loaded, cache, authority)
	}
}

func TestCachedPolicyRepositorySurvivesRedisOutage(t *testing.T) {
	record, err := testPolicyRecord(DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakePolicyCache{loadErr: errors.New("redis unavailable"), storeErr: errors.New("redis unavailable")}
	loaded, err := NewCachedPolicyRepository(&fakePolicyAuthority{record: record}, cache).LoadOrSeed(context.Background(), DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "mysql" || !loaded.CacheDegraded {
		t.Fatalf("Redis outage changed MySQL authority behavior: %+v", loaded)
	}
}

func TestDecodePolicyRecordFailsClosedOnTamperingAndUnknownFields(t *testing.T) {
	record, err := testPolicyRecord(DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	tampered := record
	tampered.PolicyJSON = strings.Replace(tampered.PolicyJSON, "routing-policy-v1", "routing-policy-v2", 1)
	if _, err := DecodePolicyRecord(tampered); err == nil {
		t.Fatal("tampered policy passed hash verification")
	}

	unknownJSON := strings.TrimSuffix(record.PolicyJSON, "}") + `,"permission":"admin"}`
	digestBytes := sha256.Sum256([]byte(unknownJSON))
	unknown := record
	unknown.PolicyJSON, unknown.PolicyHash = unknownJSON, hex.EncodeToString(digestBytes[:])
	if _, err := DecodePolicyRecord(unknown); err == nil {
		t.Fatal("unknown top-level policy field was accepted")
	}
}
