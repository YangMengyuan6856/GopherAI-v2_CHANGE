package cleanupaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/observability"
)

const SchemaVersion = "cleanup-audit-v1"

type ObservationReader interface {
	ObserveRetiredSkillAPI(context.Context) (observability.LegacyEntryObservation, error)
}

type Candidate struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Decision             string   `json:"decision"`
	Status               string   `json:"status"`
	PresentArtifacts     []string `json:"present_artifacts"`
	ExternalReferences   []string `json:"external_references"`
	ExternalReferenceCnt int      `json:"external_reference_count"`
	Replacement          string   `json:"replacement"`
	UserAuthorized       bool     `json:"user_authorized"`
	DeletionSafe         bool     `json:"deletion_safe"`
	ReasonCodes          []string `json:"reason_codes"`
}

type Summary struct {
	TotalCandidates   int  `json:"total_candidates"`
	AlreadyRemoved    int  `json:"already_removed"`
	EligibleToDelete  int  `json:"eligible_to_delete"`
	RetainedRequired  int  `json:"retained_required"`
	Blocked           int  `json:"blocked"`
	AllChecksPassed   bool `json:"all_checks_passed"`
	DeletionPlanReady bool `json:"deletion_plan_ready"`
	CleanupComplete   bool `json:"cleanup_complete"`
}

type Report struct {
	SchemaVersion         string                               `json:"schema_version"`
	ReportSHA256          string                               `json:"report_sha256"`
	ReleaseID             string                               `json:"release_id"`
	GitSHA                string                               `json:"git_sha"`
	GeneratedAt           time.Time                            `json:"generated_at"`
	SourceScope           string                               `json:"source_scope"`
	SourceInventorySHA256 string                               `json:"source_inventory_sha256"`
	TrackedSourceCount    int                                  `json:"tracked_source_count"`
	Observation           observability.LegacyEntryObservation `json:"legacy_entry_observation"`
	Summary               Summary                              `json:"summary"`
	Candidates            []Candidate                          `json:"candidates"`
	Guardrails            []string                             `json:"guardrails"`
	Limitations           []string                             `json:"limitations"`
}

type Builder struct {
	root     string
	observer ObservationReader
	clock    func() time.Time
}

func NewBuilder(root string, observer ObservationReader, clock func() time.Time) *Builder {
	if clock == nil {
		clock = time.Now
	}
	return &Builder{root: filepath.Clean(root), observer: observer, clock: clock}
}

type candidateSpec struct {
	id, title, decision, replacement string
	artifacts, needles, excludes     []string
	authorized, retained             bool
}

func (builder *Builder) Build(ctx context.Context, releaseID, gitSHA string) (Report, error) {
	if builder == nil || builder.observer == nil || strings.TrimSpace(builder.root) == "" {
		return Report{}, errors.New("cleanup audit builder is not configured")
	}
	if strings.TrimSpace(releaseID) == "" || strings.TrimSpace(gitSHA) == "" {
		return Report{}, errors.New("release identity is required")
	}
	if info, err := os.Stat(builder.root); err != nil || !info.IsDir() {
		return Report{}, errors.New("cleanup audit source root is unavailable")
	}
	trackedSources, inventorySHA, err := builder.loadSourceInventory()
	if err != nil {
		return Report{}, err
	}
	observation, err := builder.observer.ObserveRetiredSkillAPI(ctx)
	if err != nil {
		return Report{}, err
	}
	specs := []candidateSpec{
		{id: "removed_builtin_skills", title: "无场景 builtin Skill", decision: "already_removed", replacement: "governed_tool_runtime", authorized: true, artifacts: []string{"common/skills", "controller/skill", "service/skill", "dao/skill", "model/skill.go", "router/Skill.go", "vue-frontend/src/views/Skill.vue"}},
		{id: "removed_onnx_image", title: "ONNX 图片识别", decision: "already_removed", replacement: "out_of_scope_for_v0_3", authorized: true, artifacts: []string{"common/image", "controller/image", "service/image", "router/Image.go", "vue-frontend/src/views/ImageRecognition.vue"}},
		{id: "legacy_skill_api_sentinel", title: "旧 Skill API 410 观测哨兵", decision: "retain", replacement: "/api/v1/tools/catalog", retained: true, artifacts: []string{"router/router.go::LEGACY_SKILL_RETIRED"}, needles: []string{"LEGACY_SKILL_RETIRED"}, excludes: []string{"router/router.go"}},
		{id: "legacy_aihelper_tool_source", title: "旧动态 ToolSource 聚合器", decision: "delete_candidate", replacement: "internal/toolruntime.Registry", authorized: true, artifacts: []string{"common/aihelper/tool_source.go"}, needles: []string{"NewMCPToolSource", "NewCustomToolSource", "NewToolAggregator"}, excludes: []string{"common/aihelper/tool_source.go"}},
		{id: "legacy_mcp_client_package", title: "重复 MCP client 包", decision: "delete_candidate", replacement: "internal/toolruntime.MCPClient", authorized: true, artifacts: []string{"common/mcp/client/client.go"}, needles: []string{"github.com/kaitai/gopherai-mcp/client"}, excludes: []string{"common/mcp/client"}},
		{id: "generic_mcp_web_helpers", title: "未注册的通用搜索与网页读取 helper", decision: "delete_candidate", replacement: "official_document_search allowlist", authorized: true, artifacts: []string{"common/mcp/server/web_tools.go"}, needles: []string{"DuckDuckGoSearch", "FetchURLContent", "FormatSearchResults"}, excludes: []string{"common/mcp/server/web_tools.go"}},
		{id: "checked_in_backend_binary", title: "误提交的根目录 Backend 构建二进制", decision: "delete_candidate", replacement: "scripts/deploy/deploy-aliyun.ps1 local cross-build", authorized: true, artifacts: []string{"tracked::GopherAI"}},
		{id: "checked_in_mcp_binary", title: "误提交的 MCP 构建二进制", decision: "delete_candidate", replacement: "scripts/deploy/deploy-aliyun.ps1 local cross-build", authorized: true, artifacts: []string{"tracked::common/mcp/gopherai-mcp"}},
		// The deployed config file is intentionally preserved across releases and
		// may still contain this now-ignored historical TOML key. The executable
		// source contract is removed once the typed Go field disappears; unknown
		// BurntSushi/TOML keys remain backward compatible during rollout.
		{id: "legacy_mcp_base_url_config", title: "未消费的旧 mcpBaseURL Go 配置字段", decision: "delete_candidate", replacement: "fixed loopback adapter endpoint", authorized: true, artifacts: []string{"config/config.go::McpBaseURL"}, needles: []string{"McpBaseURL", "mcpBaseURL"}, excludes: []string{"config/config.go", "config/config.toml"}},
		{id: "governed_mcp_protocol_host", title: "场景化 MCP 协议边界", decision: "retain", replacement: "not_applicable", retained: true, artifacts: []string{"common/mcp/main.go", "common/mcp/server/server.go", "internal/toolruntime/mcp_adapter_tool.go"}, needles: []string{"mcp_deployment_evidence", "deployment_manifest_source"}},
	}
	report := Report{
		SchemaVersion: SchemaVersion, ReleaseID: releaseID, GitSHA: gitSHA, GeneratedAt: builder.clock().UTC(), SourceScope: "packaged_runtime_source",
		SourceInventorySHA256: inventorySHA, TrackedSourceCount: len(trackedSources),
		Observation: observation,
		Guardrails:  []string{"只扫描固定源码根与固定符号，不执行 shell", "运行调用量只读取固定 PromQL，调用方不能提交查询", "删除候选仍需独立提交、全量构建、Smoke 和可恢复 Git 历史", "MCP 协议适配器与场景化协议宿主明确保留"},
		Limitations: []string{"24 小时窗口达到至少 50% scrape 覆盖才可用于零调用判断。", "本报告只批准列出的源码候选，不批准数据库 Contract migration。", "already_removed 只证明当前树无运行产物，恢复能力来自 Git/Release 历史。"},
	}
	for _, spec := range specs {
		candidate, err := builder.inspect(spec, trackedSources)
		if err != nil {
			return Report{}, err
		}
		if spec.id == "legacy_skill_api_sentinel" && candidate.Status == "retained_required" {
			candidate.Status = "retained_observation_sentinel"
			candidate.DeletionSafe = false
			candidate.ReasonCodes = []string{"runtime_observation_required", observation.Status}
		}
		report.Candidates = append(report.Candidates, candidate)
	}
	sort.Slice(report.Candidates, func(i, j int) bool { return report.Candidates[i].ID < report.Candidates[j].ID })
	for _, candidate := range report.Candidates {
		report.Summary.TotalCandidates++
		switch candidate.Status {
		case "removed_verified":
			report.Summary.AlreadyRemoved++
		case "eligible_for_deletion":
			report.Summary.EligibleToDelete++
		case "retained_required", "retained_observation_sentinel":
			report.Summary.RetainedRequired++
		default:
			report.Summary.Blocked++
		}
	}
	report.Summary.AllChecksPassed = report.Summary.Blocked == 0 && observation.CoverageSufficient && observation.ZeroCalls
	// Independent zero-reference candidates may be removed in their own
	// reversible commit while a different candidate remains deferred.
	report.Summary.DeletionPlanReady = observation.CoverageSufficient && observation.ZeroCalls && report.Summary.EligibleToDelete > 0
	report.Summary.CleanupComplete = report.Summary.AllChecksPassed && report.Summary.EligibleToDelete == 0
	report.ReportSHA256 = reportHash(report)
	if err := Validate(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (builder *Builder) inspect(spec candidateSpec, trackedSources map[string]struct{}) (Candidate, error) {
	present, err := builder.presentArtifacts(spec.artifacts, trackedSources)
	if err != nil {
		return Candidate{}, err
	}
	references, err := builder.references(spec.needles, spec.excludes)
	if err != nil {
		return Candidate{}, err
	}
	candidate := Candidate{ID: spec.id, Title: spec.title, Decision: spec.decision, PresentArtifacts: present, ExternalReferences: references, ExternalReferenceCnt: len(references), Replacement: spec.replacement, UserAuthorized: spec.authorized}
	switch {
	case spec.retained && len(present) != len(spec.artifacts):
		candidate.Status = "blocked_required_artifact_missing"
		candidate.ReasonCodes = []string{"required_artifact_missing"}
	case spec.retained:
		candidate.Status = "retained_required"
		candidate.ReasonCodes = []string{"active_replacement_dependency"}
	case len(present) == 0:
		candidate.Status = "removed_verified"
		candidate.ReasonCodes = []string{"artifacts_absent"}
	case len(references) != 0:
		candidate.Status = "blocked_active_reference"
		candidate.ReasonCodes = []string{"external_reference_present"}
	case !spec.authorized:
		candidate.Status = "blocked_user_approval_required"
		candidate.ReasonCodes = []string{"user_approval_required"}
	default:
		candidate.Status = "eligible_for_deletion"
		candidate.DeletionSafe = true
		candidate.ReasonCodes = []string{"zero_external_references", "replacement_declared", "user_authorized"}
	}
	return candidate, nil
}

func (builder *Builder) presentArtifacts(probes []string, trackedSources map[string]struct{}) ([]string, error) {
	result := []string{}
	for _, probe := range probes {
		if strings.HasPrefix(probe, "tracked::") {
			if _, exists := trackedSources[strings.TrimPrefix(probe, "tracked::")]; exists {
				result = append(result, probe)
			}
			continue
		}
		parts := strings.SplitN(probe, "::", 2)
		path := filepath.Join(builder.root, filepath.FromSlash(parts[0]))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			hasFiles := false
			if err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() {
					hasFiles = true
				}
				return nil
			}); err != nil {
				return nil, err
			}
			if !hasFiles {
				continue
			}
		}
		if len(parts) == 2 {
			if info.IsDir() {
				return nil, errors.New("content probe points to a directory")
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(string(contents), parts[1]) {
				continue
			}
		}
		result = append(result, probe)
	}
	sort.Strings(result)
	return result, nil
}

func (builder *Builder) loadSourceInventory() (map[string]struct{}, string, error) {
	path := filepath.Join(builder.root, ".release-source-files.txt")
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) == 0 || len(contents) > 2<<20 {
		return nil, "", errors.New("tracked source inventory is unavailable")
	}
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	tracked := map[string]struct{}{}
	previous := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if filepath.IsAbs(line) || strings.Contains(line, "\\") || strings.HasPrefix(line, "../") || strings.Contains(line, "/../") || (previous != "" && previous >= line) {
			return nil, "", errors.New("tracked source inventory violated the sorted relative-path contract")
		}
		tracked[line] = struct{}{}
		previous = line
	}
	if len(tracked) == 0 {
		return nil, "", errors.New("tracked source inventory is empty")
	}
	digest := sha256.Sum256(contents)
	return tracked, hex.EncodeToString(digest[:]), nil
}

func (builder *Builder) references(needles, excludes []string) ([]string, error) {
	if len(needles) == 0 {
		return []string{}, nil
	}
	allowedRoots := []string{"common", "config", "controller", "dao", "internal", "middleware", "model", "router", "service", "scripts/deploy", "vue-frontend/src"}
	found := map[string]struct{}{}
	for _, rootName := range allowedRoots {
		root := filepath.Join(builder.root, filepath.FromSlash(rootName))
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".deploy") {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".go" && ext != ".vue" && ext != ".js" && ext != ".toml" && ext != ".ps1" {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(builder.root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			// The audit's own compile-time candidate vocabulary is evidence
			// metadata, never a runtime dependency on a retired symbol.
			if strings.HasPrefix(relative, "internal/cleanupaudit/") {
				return nil
			}
			for _, exclude := range excludes {
				if relative == exclude || strings.HasPrefix(relative, strings.TrimSuffix(exclude, "/")+"/") {
					return nil
				}
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > 2<<20 {
				return errors.New("cleanup audit source file exceeds scan limit")
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, needle := range needles {
				if strings.Contains(string(contents), needle) {
					found[relative+"::"+needle] = struct{}{}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(found))
	for reference := range found {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result, nil
}

func reportHash(report Report) string {
	report.ReportSHA256 = ""
	encoded, _ := json.Marshal(report)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion || report.GeneratedAt.IsZero() || strings.TrimSpace(report.ReleaseID) == "" || strings.TrimSpace(report.GitSHA) == "" || len(report.SourceInventorySHA256) != 64 || report.TrackedSourceCount <= 0 || len(report.ReportSHA256) != 64 || report.ReportSHA256 != reportHash(report) {
		return errors.New("cleanup audit identity or hash is invalid")
	}
	if report.Observation.SchemaVersion != "legacy-entry-observation-v1" || report.Observation.Entry != "skill_api" || report.Observation.WindowSeconds != 86400 || report.Observation.ExpectedSampleCount != 5760 || report.Observation.MinimumSampleCount != 2880 {
		return errors.New("cleanup audit observation contract is invalid")
	}
	if report.Summary.TotalCandidates != len(report.Candidates) || len(report.Candidates) != 10 {
		return errors.New("cleanup audit candidate count is invalid")
	}
	if report.Summary.CleanupComplete != (report.Summary.AllChecksPassed && report.Summary.EligibleToDelete == 0) || report.Summary.CleanupComplete && report.Summary.DeletionPlanReady {
		return errors.New("cleanup audit terminal state is inconsistent")
	}
	seen := map[string]struct{}{}
	for index, candidate := range report.Candidates {
		if _, duplicate := seen[candidate.ID]; duplicate || candidate.ID == "" || candidate.Title == "" || candidate.Status == "" || candidate.Replacement == "" || candidate.ExternalReferenceCnt != len(candidate.ExternalReferences) {
			return errors.New("cleanup audit candidate is invalid")
		}
		seen[candidate.ID] = struct{}{}
		if index > 0 && report.Candidates[index-1].ID >= candidate.ID {
			return errors.New("cleanup audit candidates are not stably ordered")
		}
		if candidate.DeletionSafe && candidate.Status != "eligible_for_deletion" {
			return errors.New("cleanup audit marked a non-eligible candidate safe")
		}
	}
	return nil
}
