package evolution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"GopherAI/internal/failurepool"
	"GopherAI/model"
)

type candidateTemplate struct {
	artifactType, parentVersion, path, rawValue string
}

var candidateTemplates = map[string]candidateTemplate{
	"prompt:grounded_answer_contract":      {"prompt_template", "grounded-answer-contract-v1", "/response_contract/minimum_citations", "2"},
	"prompt:resolution_output_contract":    {"prompt_template", "resolution-output-contract-v1", "/response_contract/require_verification_step", "true"},
	"parameter:context_budget_allocation":  {"context_policy", "context-assembler-v2", "/budgets/evidence_token_limit", "4500"},
	"rule:tool_failure_recovery_rule":      {"diagnostic_playbook", "diagnostic-playbook-v1", "/tool_failure/require_secondary_evidence", "true"},
	"rule:request_fallback_classification": {"diagnostic_playbook", "diagnostic-playbook-v1", "/request_error/clarify_before_fallback", "true"},
}

var (
	ErrInvalidLineage     = errors.New("failure proposal lineage is invalid")
	ErrNotHarnessArtifact = errors.New("proposal is not an allowed harness artifact")
)

var allowedPatchPaths = map[string]map[string]struct{}{
	"prompt_template": {
		"/response_contract/minimum_citations": {}, "/response_contract/require_verification_step": {},
	},
	"context_policy": {
		"/budgets/evidence_token_limit": {},
	},
	"diagnostic_playbook": {
		"/tool_failure/require_secondary_evidence": {}, "/request_error/clarify_before_fallback": {},
	},
}

func BuildCandidate(run model.FailureMiningRun, cluster model.FailureCluster, proposal model.FailureImprovementProposal) (model.HarnessArtifact, error) {
	if len(run.ID) != 64 || len(run.InputHash) != 64 || run.SchemaVersion != failurepool.SchemaVersion || run.Status != failurepool.StatusCompleted ||
		len(cluster.ID) != 64 || cluster.RunID != run.ID || cluster.SchemaVersion != failurepool.SchemaVersion || cluster.Status != failurepool.StatusPendingReview || cluster.SampleCount <= 0 || len(cluster.SampleSetHash) != 64 ||
		len(proposal.ID) != 64 || proposal.ID != proposal.ProposalHash || proposal.ClusterID != cluster.ID || proposal.RunID != run.ID || proposal.SchemaVersion != failurepool.SchemaVersion ||
		proposal.State != failurepool.StatusPendingReview || !proposal.RequiresHumanReview || proposal.OfflineGatePassed || proposal.IsolationCanaryPassed || proposal.Applied || proposal.CreatedAt.IsZero() {
		return model.HarnessArtifact{}, ErrInvalidLineage
	}
	template, ok := candidateTemplates[proposal.CandidateKind+":"+proposal.Target]
	if !ok {
		return model.HarnessArtifact{}, ErrNotHarnessArtifact
	}
	patch := MinimalPatch{Operation: "replace", Path: template.path, Value: json.RawMessage(template.rawValue), VariableCount: 1}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return model.HarnessArtifact{}, err
	}
	patchSHA := digestBytes(patchJSON)
	budgetJSON := `{"model_calls":0,"token_budget":0,"search_iterations":1}`
	artifactVersion := fmt.Sprintf("%s-candidate-%s", template.artifactType, proposal.ProposalHash[:12])
	artifact := model.HarnessArtifact{
		SchemaVersion: SchemaVersion, ArtifactType: template.artifactType, ArtifactVersion: artifactVersion,
		ParentVersion: template.parentVersion, ParentSHA256: digestString(template.parentVersion), PatchJSON: string(patchJSON), PatchSHA256: patchSHA,
		TargetClusterID: cluster.ID, SourceProposalID: proposal.ID, SourceProposalSHA256: proposal.ProposalHash,
		ProposerVersion: ProposerVersion, ValidatorVersion: ValidatorVersion, DataSplit: DataSplit, DataVersion: run.ID, DataSHA256: cluster.SampleSetHash,
		BudgetJSON: budgetJSON, Status: StatusValidated, StaticValidationPassed: true, RequiresHumanApproval: true,
		OfflineEvaluationPassed: false, HoldoutOpened: false, Applied: false, RollbackVersion: template.parentVersion, CreatedAt: proposal.CreatedAt.UTC(),
	}
	identity, err := artifactIdentity(artifact)
	if err != nil {
		return model.HarnessArtifact{}, err
	}
	artifact.ID, artifact.ArtifactSHA256 = identity, identity
	if err := ValidateCandidate(artifact); err != nil {
		return model.HarnessArtifact{}, err
	}
	return artifact, nil
}

func ValidateCandidate(artifact model.HarnessArtifact) error {
	if artifact.SchemaVersion != SchemaVersion || artifact.Status != StatusValidated || artifact.ID == "" || artifact.ID != artifact.ArtifactSHA256 ||
		len(artifact.ID) != 64 || len(artifact.ParentSHA256) != 64 || len(artifact.PatchSHA256) != 64 || len(artifact.TargetClusterID) != 64 ||
		len(artifact.SourceProposalID) != 64 || artifact.SourceProposalID != artifact.SourceProposalSHA256 || len(artifact.DataVersion) != 64 || len(artifact.DataSHA256) != 64 ||
		artifact.ProposerVersion != ProposerVersion || artifact.ValidatorVersion != ValidatorVersion || artifact.DataSplit != DataSplit || artifact.CreatedAt.IsZero() ||
		!artifact.StaticValidationPassed || !artifact.RequiresHumanApproval || artifact.OfflineEvaluationPassed || artifact.HoldoutOpened || artifact.Applied ||
		artifact.RollbackVersion != artifact.ParentVersion {
		return errors.New("harness candidate envelope is invalid")
	}
	var patch MinimalPatch
	decoder := json.NewDecoder(strings.NewReader(artifact.PatchJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return errors.New("harness candidate patch is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || patch.Operation != "replace" || patch.VariableCount != 1 || !json.Valid(patch.Value) {
		return errors.New("harness candidate patch is invalid")
	}
	paths := allowedPatchPaths[artifact.ArtifactType]
	if _, allowed := paths[patch.Path]; !allowed {
		return errors.New("harness candidate patch path is forbidden")
	}
	lower := strings.ToLower(patch.Path + " " + string(patch.Value))
	for _, forbidden := range []string{"source_code", "tool_permission", "security_policy", "database", "migration", "secret", "deployment", "external_write"} {
		if strings.Contains(lower, forbidden) {
			return errors.New("harness candidate crosses a forbidden boundary")
		}
	}
	if digestString(artifact.PatchJSON) != artifact.PatchSHA256 || digestString(artifact.ParentVersion) != artifact.ParentSHA256 {
		return errors.New("harness candidate component hash mismatch")
	}
	budget, err := decodeBudget(artifact.BudgetJSON)
	if err != nil || budget != (ExecutionBudget{ModelCalls: 0, TokenBudget: 0, SearchIterations: 1}) {
		return errors.New("harness candidate budget is invalid")
	}
	expected, err := artifactIdentity(artifact)
	if err != nil || expected != artifact.ArtifactSHA256 {
		return errors.New("harness candidate artifact hash mismatch")
	}
	return nil
}

func artifactIdentity(artifact model.HarnessArtifact) (string, error) {
	artifact.ID, artifact.ArtifactSHA256, artifact.CreatedAt = "", "", time.Time{}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func decodePatch(raw string) (MinimalPatch, error) {
	var patch MinimalPatch
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return patch, err
	}
	return patch, nil
}

func decodeBudget(raw string) (ExecutionBudget, error) {
	var budget ExecutionBudget
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&budget); err != nil {
		return budget, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return budget, errors.New("harness candidate budget contains trailing content")
	}
	return budget, nil
}

func digestString(value string) string { return digestBytes([]byte(value)) }

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
