package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLiveDoesNotCheckDependencies(t *testing.T) {
	called := false
	service := NewService("gopherai", time.Second, []Probe{{
		Name:     "redis",
		Required: true,
		Check: func(context.Context) error {
			called = true
			return errors.New("unavailable")
		},
	}})

	response := service.Live()
	if response.Status != StatusAlive {
		t.Fatalf("expected %q, got %q", StatusAlive, response.Status)
	}
	if called {
		t.Fatal("live endpoint must not check downstream dependencies")
	}
}

func TestReadyReportsRequiredDependencyFailure(t *testing.T) {
	service := NewService("gopherai", time.Second, []Probe{
		{Name: "mysql", Required: true, Check: func(context.Context) error { return nil }},
		{Name: "redis_cache", Required: true, Check: func(context.Context) error { return errors.New("down") }},
	})

	response, ready := service.Ready(context.Background())
	if ready {
		t.Fatal("expected service to be not ready")
	}
	if response.Status != StatusNotReady {
		t.Fatalf("expected %q, got %q", StatusNotReady, response.Status)
	}
	if response.Components["mysql"].Status != StatusUp {
		t.Fatalf("expected mysql up, got %#v", response.Components["mysql"])
	}
	redisStatus := response.Components["redis_cache"]
	if redisStatus.Status != StatusDown || redisStatus.ErrorCode != "dependency_unavailable" {
		t.Fatalf("unexpected redis status: %#v", redisStatus)
	}
}

func TestReadyAllowsOptionalDependencyFailure(t *testing.T) {
	service := NewService("gopherai", time.Second, []Probe{
		{Name: "mysql", Required: true, Check: func(context.Context) error { return nil }},
		{Name: "optional", Required: false, Check: func(context.Context) error { return errors.New("down") }},
	})

	response, ready := service.Ready(context.Background())
	if !ready {
		t.Fatal("optional dependency must not block readiness")
	}
	if response.Status != StatusDegraded {
		t.Fatalf("expected %q, got %q", StatusDegraded, response.Status)
	}
}

func TestReadyClassifiesProbeTimeout(t *testing.T) {
	service := NewService("gopherai", 10*time.Millisecond, []Probe{{
		Name:     "slow",
		Required: true,
		Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}})

	response, ready := service.Ready(context.Background())
	if ready {
		t.Fatal("timed out dependency must block readiness")
	}
	if response.Components["slow"].ErrorCode != "timeout" {
		t.Fatalf("expected timeout, got %#v", response.Components["slow"])
	}
}
