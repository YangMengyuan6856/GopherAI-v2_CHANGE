package toolruntime

import (
	"errors"
	"sort"
	"sync"
)

var ErrToolAlreadyRegistered = errors.New("tool is already registered")

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry { return &Registry{tools: make(map[string]Tool)} }

func (registry *Registry) Register(tool Tool) error {
	if registry == nil || tool == nil {
		return errors.New("registry and tool are required")
	}
	definition := tool.Definition()
	if err := validateDefinition(definition); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.tools[definition.Name]; exists {
		return ErrToolAlreadyRegistered
	}
	registry.tools[definition.Name] = tool
	return nil
}

// Lookup is deliberately exact. Unknown or misspelled names are never guessed.
func (registry *Registry) Lookup(name string) (Tool, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	tool, ok := registry.tools[name]
	return tool, ok
}

func (registry *Registry) Definitions() []Definition {
	if registry == nil {
		return []Definition{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	definitions := make([]Definition, 0, len(registry.tools))
	for _, tool := range registry.tools {
		definitions = append(definitions, tool.Definition())
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}
