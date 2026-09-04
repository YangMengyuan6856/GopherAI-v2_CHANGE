package feature

import "testing"

func TestDefaultProviderEnablesDevSupport(t *testing.T) {
	provider := DefaultProvider()
	provider.lookup = func(string) (string, bool) { return "", false }
	if !provider.Enabled(DevSupportEnabled) {
		t.Fatal("devsupport must default to enabled for the new auto entry")
	}
	if !provider.Enabled(RAGFastEnabled) {
		t.Fatal("rag_fast must default to enabled behind its explicit request gate")
	}
}

func TestEnvironmentOverridesDefault(t *testing.T) {
	provider := DefaultProvider()
	provider.lookup = func(name string) (string, bool) {
		if name != "GOPHERAI_FEATURE_DEVSUPPORT_ENABLED" {
			t.Fatalf("unexpected environment name %q", name)
		}
		return "false", true
	}
	if provider.Enabled(DevSupportEnabled) {
		t.Fatal("expected environment override to disable feature")
	}
}

func TestInvalidEnvironmentValueFallsBackToDefault(t *testing.T) {
	provider := DefaultProvider()
	provider.lookup = func(string) (string, bool) { return "maybe", true }
	if !provider.Enabled(DevSupportEnabled) {
		t.Fatal("invalid override must not unexpectedly disable the feature")
	}
}
