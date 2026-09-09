package toolruntime

import (
	"strings"
	"sync"
)

// ActionGuard is deliberately in-memory and request-scoped. It is not a
// distributed lock or an idempotency store: its only job is to stop a single
// Agent run from repeating an identical canonical tool action forever.
type ActionGuard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewActionGuard() *ActionGuard {
	return &ActionGuard{seen: make(map[string]struct{})}
}

func (guard *ActionGuard) reserve(toolName string, toolVersion string, argsHash string) bool {
	if guard == nil {
		return true
	}
	signature := strings.Join([]string{toolName, toolVersion, argsHash}, "|")
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.seen == nil {
		guard.seen = make(map[string]struct{})
	}
	if _, exists := guard.seen[signature]; exists {
		return false
	}
	guard.seen[signature] = struct{}{}
	return true
}
