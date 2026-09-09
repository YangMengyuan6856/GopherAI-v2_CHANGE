package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRetiredMCPBaseURLRemainsBackwardCompatibleDuringRollingDeploy(t *testing.T) {
	var decoded Config
	metadata, err := toml.Decode("[reactConfig]\nmaxIterations = 7\nmcpBaseURL = \"http://legacy.invalid/mcp\"\n", &decoded)
	if err != nil {
		t.Fatalf("preserved runtime config must remain decodable: %v", err)
	}
	if decoded.ReactConfig.MaxIterations != 7 {
		t.Fatalf("known React setting was not decoded: %+v", decoded.ReactConfig)
	}
	if len(metadata.Undecoded()) != 1 || metadata.Undecoded()[0].String() != "reactConfig.mcpBaseURL" {
		t.Fatalf("retired key must be ignored explicitly, got undecoded keys: %v", metadata.Undecoded())
	}
}
