package aihelper

import (
	"context"
	"strings"
	"testing"
)

func TestLegacyDemoStrategiesAreNotRegistered(t *testing.T) {
	factory := GetGlobalFactory()
	for _, modelType := range []string{"3", "5"} {
		_, err := factory.CreateAIModel(context.Background(), modelType, map[string]interface{}{
			"username": "test-user",
		})
		if err == nil {
			t.Fatalf("legacy model type %s must not be registered", modelType)
		}
		if !strings.Contains(err.Error(), "unsupported model type") {
			t.Fatalf("unexpected error for model type %s: %v", modelType, err)
		}
	}
}
