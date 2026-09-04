package feature

import (
	"os"
	"strconv"
	"strings"
)

const (
	DevSupportEnabled = "devsupport.enabled"
	RAGFastEnabled    = "rag.fast.enabled"
)

type Provider interface {
	Enabled(name string) bool
}

type EnvProvider struct {
	defaults map[string]bool
	lookup   func(string) (string, bool)
}

func NewEnvProvider(defaults map[string]bool) *EnvProvider {
	copyOfDefaults := make(map[string]bool, len(defaults))
	for name, enabled := range defaults {
		copyOfDefaults[name] = enabled
	}
	return &EnvProvider{defaults: copyOfDefaults, lookup: os.LookupEnv}
}

func DefaultProvider() *EnvProvider {
	return NewEnvProvider(map[string]bool{DevSupportEnabled: true, RAGFastEnabled: true})
}

func (provider *EnvProvider) Enabled(name string) bool {
	defaultValue := false
	if provider != nil {
		defaultValue = provider.defaults[name]
	}
	if provider == nil || provider.lookup == nil {
		return defaultValue
	}
	value, exists := provider.lookup(environmentName(name))
	if !exists {
		return defaultValue
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return defaultValue
	}
	return enabled
}

func environmentName(name string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	return "GOPHERAI_FEATURE_" + strings.ToUpper(replacer.Replace(name))
}
