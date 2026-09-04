package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const manifestFileLimit = 64 * 1024

type DeploymentManifestTool struct{ path string }

type deploymentManifest struct {
	ReleaseID          string   `json:"release_id"`
	Branch             string   `json:"branch"`
	GitSHA             string   `json:"git_sha"`
	SourceDirty        bool     `json:"source_dirty"`
	BuiltAt            string   `json:"built_at"`
	BuildStrategy      string   `json:"build_strategy"`
	Target             string   `json:"target"`
	GoVersion          string   `json:"go_version"`
	IncludedComponents []string `json:"included_components"`
	ConfigIncluded     bool     `json:"config_included"`
	Migrations         []string `json:"migrations"`
	Rollback           string   `json:"rollback"`
}

type PublicDeploymentManifest struct {
	ReleaseID          string   `json:"release_id"`
	Branch             string   `json:"branch"`
	GitSHA             string   `json:"git_sha"`
	SourceDirty        bool     `json:"source_dirty"`
	BuiltAt            string   `json:"built_at"`
	BuildStrategy      string   `json:"build_strategy"`
	Target             string   `json:"target"`
	GoVersion          string   `json:"go_version"`
	IncludedComponents []string `json:"included_components"`
	ConfigIncluded     bool     `json:"config_included"`
	Migrations         []string `json:"migrations"`
	Rollback           string   `json:"rollback"`
}

func NewDeploymentManifestTool(path string) *DeploymentManifestTool {
	return &DeploymentManifestTool{path: path}
}

func (tool *DeploymentManifestTool) Definition() Definition {
	return Definition{
		Name: "deployment_manifest_lookup", Version: "1.0.0",
		Description:    "读取当前已部署发布包的受控清单，返回版本、构建方式、组件和回滚策略；不接受任意文件路径。",
		InputSchema:    InputSchema{Type: "object", Properties: map[string]PropertySchema{}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task", "troubleshooting"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 1000, MaxResultBytes: 8192,
	}
}

func (tool *DeploymentManifestTool) Execute(ctx context.Context, _ map[string]any) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	file, err := os.Open(tool.path)
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("open deployment manifest: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, manifestFileLimit+1))
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("read deployment manifest: %w", err)
	}
	if len(contents) > manifestFileLimit {
		return Output{}, errors.New("deployment manifest exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest deploymentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return Output{}, fmt.Errorf("decode deployment manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Output{}, fmt.Errorf("decode deployment manifest: %w", err)
	}
	if strings.TrimSpace(manifest.ReleaseID) == "" || strings.TrimSpace(manifest.GitSHA) == "" || strings.TrimSpace(manifest.Branch) == "" {
		return Output{}, errors.New("deployment manifest misses required identity fields")
	}
	public := PublicDeploymentManifest(manifest)
	return Output{Data: public, EvidenceRefs: []string{"release-manifest:" + manifest.ReleaseID}}, nil
}
