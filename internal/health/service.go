package health

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	StatusAlive    = "alive"
	StatusReady    = "ready"
	StatusDegraded = "degraded"
	StatusNotReady = "not_ready"
	StatusUp       = "up"
	StatusDown     = "down"
)

// Probe describes one runtime dependency checked by the readiness endpoint.
// Required probes decide whether the process is ready to receive traffic.
type Probe struct {
	Name     string
	Required bool
	Check    func(context.Context) error
}

type ComponentStatus struct {
	Status    string `json:"status"`
	Required  bool   `json:"required"`
	LatencyMS int64  `json:"latency_ms"`
	ErrorCode string `json:"error_code,omitempty"`
}

type LiveResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	CheckedAt time.Time `json:"checked_at"`
}

type ReadyResponse struct {
	Status     string                     `json:"status"`
	Service    string                     `json:"service"`
	CheckedAt  time.Time                  `json:"checked_at"`
	Components map[string]ComponentStatus `json:"components"`
}

type Service struct {
	serviceName string
	timeout     time.Duration
	probes      []Probe
}

func NewService(serviceName string, timeout time.Duration, probes []Probe) *Service {
	if timeout <= 0 {
		timeout = time.Second
	}
	return &Service{
		serviceName: serviceName,
		timeout:     timeout,
		probes:      append([]Probe(nil), probes...),
	}
}

func (s *Service) Live() LiveResponse {
	return LiveResponse{
		Status:    StatusAlive,
		Service:   s.serviceName,
		CheckedAt: time.Now().UTC(),
	}
}

// Ready checks dependencies concurrently so one slow dependency does not add
// its timeout to every other dependency's latency.
func (s *Service) Ready(ctx context.Context) (ReadyResponse, bool) {
	components := make(map[string]ComponentStatus, len(s.probes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, configuredProbe := range s.probes {
		probe := configuredProbe
		wg.Add(1)
		go func() {
			defer wg.Done()

			startedAt := time.Now()
			status := ComponentStatus{Status: StatusUp, Required: probe.Required}
			probeContext, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()

			var err error
			if probe.Name == "" || probe.Check == nil {
				err = errors.New("invalid health probe")
			} else {
				err = probe.Check(probeContext)
			}
			status.LatencyMS = time.Since(startedAt).Milliseconds()
			if err != nil {
				status.Status = StatusDown
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeContext.Err(), context.DeadlineExceeded) {
					status.ErrorCode = "timeout"
				} else {
					status.ErrorCode = "dependency_unavailable"
				}
			}

			mu.Lock()
			components[probe.Name] = status
			mu.Unlock()
		}()
	}
	wg.Wait()

	ready := true
	degraded := false
	for _, component := range components {
		if component.Status == StatusDown {
			degraded = true
			if component.Required {
				ready = false
			}
		}
	}

	status := StatusReady
	if !ready {
		status = StatusNotReady
	} else if degraded {
		status = StatusDegraded
	}

	return ReadyResponse{
		Status:     status,
		Service:    s.serviceName,
		CheckedAt:  time.Now().UTC(),
		Components: components,
	}, ready
}
