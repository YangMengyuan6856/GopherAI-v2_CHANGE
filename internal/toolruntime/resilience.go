package toolruntime

import (
	"encoding/json"
	"sync"
	"time"
)

type cachedToolResult struct {
	data         json.RawMessage
	evidenceRefs []string
	expiresAt    time.Time
}

type memoryToolCache struct {
	mu      sync.RWMutex
	entries map[string]cachedToolResult
}

func newMemoryToolCache() *memoryToolCache {
	return &memoryToolCache{entries: make(map[string]cachedToolResult)}
}

type cacheFreshness string

const (
	cacheMiss  cacheFreshness = "miss"
	cacheFresh cacheFreshness = "fresh"
	cacheStale cacheFreshness = "stale"
)

func (cache *memoryToolCache) get(key string, now time.Time, staleIfError time.Duration) (cachedToolResult, cacheFreshness) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[key]
	if !ok {
		return cachedToolResult{}, cacheMiss
	}
	freshness := cacheFresh
	if !now.Before(entry.expiresAt) {
		if staleIfError <= 0 || !now.Before(entry.expiresAt.Add(staleIfError)) {
			delete(cache.entries, key)
			return cachedToolResult{}, cacheMiss
		}
		freshness = cacheStale
	}
	entry.data = append(json.RawMessage(nil), entry.data...)
	entry.evidenceRefs = append([]string(nil), entry.evidenceRefs...)
	return entry, freshness
}

func (cache *memoryToolCache) put(key string, entry cachedToolResult, expiresAt time.Time) {
	entry.data = append(json.RawMessage(nil), entry.data...)
	entry.evidenceRefs = append([]string(nil), entry.evidenceRefs...)
	entry.expiresAt = expiresAt
	cache.mu.Lock()
	cache.entries[key] = entry
	cache.mu.Unlock()
}

func toolCacheKey(definition Definition, invocation Invocation, argsHash string) string {
	return definition.Name + "@" + definition.Version + ":" + argsHash + ":" + hashPrincipal(invocation.Principal.TenantID) + ":" + hashPrincipal(invocation.Principal.UserID)
}

type breakerState struct {
	state         string
	failures      int
	openedAt      time.Time
	probeInFlight bool
}

type circuitSet struct {
	mu     sync.Mutex
	states map[string]*breakerState
}

func newCircuitSet() *circuitSet { return &circuitSet{states: make(map[string]*breakerState)} }

func (circuits *circuitSet) allow(definition Definition, now time.Time) (bool, string) {
	if definition.CircuitFailures == 0 {
		return true, ""
	}
	circuits.mu.Lock()
	defer circuits.mu.Unlock()
	state := circuits.state(definition.Name)
	switch state.state {
	case "open":
		if now.Sub(state.openedAt) < time.Duration(definition.CircuitOpenMS)*time.Millisecond {
			return false, ""
		}
		state.state, state.probeInFlight = "half_open", true
		return true, "half_open"
	case "half_open":
		if state.probeInFlight {
			return false, ""
		}
		state.probeInFlight = true
	}
	return true, ""
}

func (circuits *circuitSet) success(definition Definition) string {
	if definition.CircuitFailures == 0 {
		return ""
	}
	circuits.mu.Lock()
	defer circuits.mu.Unlock()
	state := circuits.state(definition.Name)
	transition := ""
	if state.state != "closed" {
		transition = "closed"
	}
	state.state, state.failures, state.probeInFlight = "closed", 0, false
	return transition
}

func (circuits *circuitSet) failure(definition Definition, now time.Time) string {
	if definition.CircuitFailures == 0 {
		return ""
	}
	circuits.mu.Lock()
	defer circuits.mu.Unlock()
	state := circuits.state(definition.Name)
	state.probeInFlight = false
	state.failures++
	if state.state == "half_open" || state.failures >= definition.CircuitFailures {
		state.state, state.openedAt = "open", now
		return "open"
	}
	return ""
}

func (circuits *circuitSet) state(tool string) *breakerState {
	state, ok := circuits.states[tool]
	if !ok {
		state = &breakerState{state: "closed"}
		circuits.states[tool] = state
	}
	return state
}
