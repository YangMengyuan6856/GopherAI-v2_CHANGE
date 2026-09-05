package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	SynthesisSchemaVersion = "collaboration-synthesis-shadow-v1"
	SynthesizerVersion     = "evidence-aware-synthesizer-v1"
	SynthesisComplete      = "complete"
	SynthesisPartial       = "partial"
	SynthesisConflict      = "conflict"
	SynthesisInsufficient  = "insufficient"
	ClaimSupported         = "supported"
	ClaimConflicted        = "conflicted"
	maxSynthesizedEvidence = 30
	maxSynthesizedClaims   = 20
)

type SynthesizedCitation struct {
	CitationID    string `json:"citation_id"`
	EvidenceID    string `json:"evidence_id"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	LineStart     int    `json:"line_start,omitempty"`
	LineEnd       int    `json:"line_end,omitempty"`
}

type SynthesizedClaim struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Statement     string   `json:"statement"`
	Confidence    float64  `json:"confidence"`
	Status        string   `json:"status"`
	SourceAgents  []string `json:"source_agents"`
	EvidenceRefs  []string `json:"evidence_refs"`
	CitationIDs   []string `json:"citation_ids"`
	ConflictKey   string   `json:"conflict_key,omitempty"`
	ConflictValue string   `json:"conflict_value,omitempty"`
}

type RejectedClaim struct {
	ClaimID    string `json:"claim_id"`
	Agent      string `json:"agent"`
	ReasonCode string `json:"reason_code"`
}

type ClaimConflict struct {
	ConflictKey string   `json:"conflict_key"`
	ClaimIDs    []string `json:"claim_ids"`
	Values      []string `json:"values"`
	ReasonCode  string   `json:"reason_code"`
}

type SynthesisResult struct {
	SchemaVersion      string                `json:"schema_version"`
	SynthesizerVersion string                `json:"synthesizer_version"`
	Mode               string                `json:"mode"`
	AffectsLiveTraffic bool                  `json:"affects_live_traffic"`
	Status             string                `json:"status"`
	ReasonCode         string                `json:"reason_code"`
	UnifiedAnswer      string                `json:"unified_answer"`
	Claims             []SynthesizedClaim    `json:"claims"`
	Conflicts          []ClaimConflict       `json:"conflicts"`
	RejectedClaims     []RejectedClaim       `json:"rejected_claims"`
	Evidence           []SharedEvidence      `json:"evidence"`
	Citations          []SynthesizedCitation `json:"citations"`
	DegradedAgents     []string              `json:"degraded_agents"`
	FallbackStrategy   string                `json:"fallback_strategy,omitempty"`
}

type EvidenceAwareSynthesizer struct{}

func NewEvidenceAwareSynthesizer() *EvidenceAwareSynthesizer {
	return new(EvidenceAwareSynthesizer)
}

type evidenceCollection struct {
	items      map[string]SharedEvidence
	order      []string
	collisions map[string]struct{}
	overflow   map[string]struct{}
}

func (*EvidenceAwareSynthesizer) Synthesize(ctx context.Context, execution ExecutionResult, tenantID string) (SynthesisResult, error) {
	result := SynthesisResult{
		SchemaVersion: SynthesisSchemaVersion, SynthesizerVersion: SynthesizerVersion,
		Mode: "shadow_only", AffectsLiveTraffic: false, Status: SynthesisInsufficient,
		ReasonCode: "no_supported_claims", Claims: []SynthesizedClaim{}, Conflicts: []ClaimConflict{},
		RejectedClaims: []RejectedClaim{}, Evidence: []SharedEvidence{}, Citations: []SynthesizedCitation{}, DegradedAgents: []string{},
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return result, errors.New("tenant is required")
	}
	if execution.SchemaVersion != ExecutionSchemaVersion || execution.Mode != "shadow_only" || execution.AffectsLiveTraffic || len(execution.TaskResults) > maximumPlannedAgents {
		return result, errors.New("execution boundary is invalid")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	tasks := append([]TaskExecution{}, execution.TaskResults...)
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Index < tasks[j].Index })
	for index := range tasks {
		if tasks[index].Status == TaskStatusSucceeded {
			if err := validateAgentOutput(tasks[index].Output, tenantID); err != nil {
				tasks[index].Status = TaskStatusFailed
				tasks[index].ReasonCode = "synthesis_input_invalid"
				tasks[index].Output = emptyAgentOutput()
			}
		}
	}
	evidence := collectEvidence(tasks, tenantID)
	claims, rejected := collectClaims(tasks, evidence)
	if len(claims) > maxSynthesizedClaims {
		for _, claim := range claims[maxSynthesizedClaims:] {
			rejected = append(rejected, RejectedClaim{ClaimID: claim.ID, Agent: strings.Join(claim.SourceAgents, ","), ReasonCode: "claim_limit_exceeded"})
		}
		claims = claims[:maxSynthesizedClaims]
	}
	conflicts := detectClaimConflicts(claims)
	citations, admittedEvidence := buildCitations(claims, evidence)
	assignCitationIDs(claims, citations)
	degradedAgents := failedAgents(tasks)
	result.Claims, result.Conflicts, result.RejectedClaims = claims, conflicts, rejected
	result.Evidence, result.Citations, result.DegradedAgents = admittedEvidence, citations, degradedAgents
	result.Status, result.ReasonCode, result.FallbackStrategy = synthesisOutcome(claims, conflicts, degradedAgents)
	result.UnifiedAnswer = compileUnifiedAnswer(result.Status, claims)
	if err := ctx.Err(); err != nil {
		return SynthesisResult{}, err
	}
	return result, nil
}

func collectEvidence(tasks []TaskExecution, tenantID string) evidenceCollection {
	collection := evidenceCollection{
		items: make(map[string]SharedEvidence), collisions: make(map[string]struct{}), overflow: make(map[string]struct{}), order: []string{},
	}
	for _, task := range tasks {
		if task.Status != TaskStatusSucceeded {
			continue
		}
		for _, candidate := range task.Output.Evidence {
			candidate.ID = strings.TrimSpace(candidate.ID)
			if candidate.ID == "" || strings.TrimSpace(candidate.TenantID) != tenantID {
				collection.collisions[candidate.ID] = struct{}{}
				delete(collection.items, candidate.ID)
				continue
			}
			if _, collided := collection.collisions[candidate.ID]; collided {
				continue
			}
			if current, exists := collection.items[candidate.ID]; exists {
				if !sameEvidenceIdentity(current, candidate) {
					collection.collisions[candidate.ID] = struct{}{}
					delete(collection.items, candidate.ID)
					continue
				}
				if candidate.Score > current.Score {
					current.Score = candidate.Score
					collection.items[candidate.ID] = current
				}
				continue
			}
			if len(collection.items) >= maxSynthesizedEvidence {
				collection.overflow[candidate.ID] = struct{}{}
				continue
			}
			collection.items[candidate.ID] = candidate
			collection.order = append(collection.order, candidate.ID)
		}
	}
	return collection
}

func sameEvidenceIdentity(left, right SharedEvidence) bool {
	return left.ID == right.ID && left.SourceType == right.SourceType && left.Summary == right.Summary &&
		left.TenantID == right.TenantID && left.SourceID == right.SourceID && left.SourceVersion == right.SourceVersion &&
		left.LineStart == right.LineStart && left.LineEnd == right.LineEnd && left.ContentHash == right.ContentHash
}

func collectClaims(tasks []TaskExecution, evidence evidenceCollection) ([]SynthesizedClaim, []RejectedClaim) {
	claims := make([]SynthesizedClaim, 0, maxSynthesizedClaims)
	rejected := []RejectedClaim{}
	canonicalIndex := make(map[string]int)
	claimIDs := make(map[string]string)
	for _, task := range tasks {
		if task.Status != TaskStatusSucceeded {
			continue
		}
		for _, candidate := range task.Output.Claims {
			candidate.ID, candidate.Statement = strings.TrimSpace(candidate.ID), strings.TrimSpace(candidate.Statement)
			signature := normalizedClaim(candidate.Kind, candidate.Statement)
			if prior, exists := claimIDs[candidate.ID]; exists && prior != signature {
				rejected = append(rejected, RejectedClaim{ClaimID: candidate.ID, Agent: task.Agent, ReasonCode: "claim_id_collision"})
				continue
			}
			claimIDs[candidate.ID] = signature
			if len(candidate.EvidenceRefs) == 0 {
				rejected = append(rejected, RejectedClaim{ClaimID: candidate.ID, Agent: task.Agent, ReasonCode: "claim_has_no_citation"})
				continue
			}
			validReferences, reason := validateClaimReferences(candidate.EvidenceRefs, evidence)
			if reason != "" {
				rejected = append(rejected, RejectedClaim{ClaimID: candidate.ID, Agent: task.Agent, ReasonCode: reason})
				continue
			}
			canonical := normalizedClaim(candidate.Kind, candidate.Statement)
			if index, exists := canonicalIndex[canonical]; exists {
				if strings.TrimSpace(candidate.ConflictKey) != claims[index].ConflictKey || strings.TrimSpace(candidate.ConflictValue) != claims[index].ConflictValue {
					rejected = append(rejected, RejectedClaim{ClaimID: candidate.ID, Agent: task.Agent, ReasonCode: "claim_conflict_metadata_mismatch"})
					continue
				}
				claims[index].SourceAgents = appendUniqueSorted(claims[index].SourceAgents, task.Agent)
				claims[index].EvidenceRefs = appendUniqueStable(claims[index].EvidenceRefs, validReferences...)
				if candidate.Confidence > claims[index].Confidence {
					claims[index].Confidence = candidate.Confidence
				}
				continue
			}
			claim := SynthesizedClaim{
				ID: candidate.ID, Kind: strings.TrimSpace(candidate.Kind), Statement: candidate.Statement,
				Confidence: candidate.Confidence, Status: ClaimSupported, SourceAgents: []string{task.Agent},
				EvidenceRefs: validReferences, CitationIDs: []string{},
				ConflictKey: strings.TrimSpace(candidate.ConflictKey), ConflictValue: strings.TrimSpace(candidate.ConflictValue),
			}
			canonicalIndex[canonical] = len(claims)
			claims = append(claims, claim)
		}
	}
	return claims, rejected
}

func normalizedClaim(kind string, statement string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(kind)), " ") + "\x00" + strings.Join(strings.Fields(strings.TrimSpace(statement)), " "))
}

func validateClaimReferences(references []string, evidence evidenceCollection) ([]string, string) {
	valid := make([]string, 0, len(references))
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if _, collided := evidence.collisions[reference]; collided {
			return nil, "evidence_id_collision"
		}
		if _, overflow := evidence.overflow[reference]; overflow {
			return nil, "evidence_limit_exceeded"
		}
		if _, exists := evidence.items[reference]; !exists {
			return nil, "unknown_evidence_reference"
		}
		valid = appendUniqueStable(valid, reference)
	}
	return valid, ""
}

func detectClaimConflicts(claims []SynthesizedClaim) []ClaimConflict {
	groups := make(map[string][]int)
	keys := []string{}
	for index, claim := range claims {
		key := strings.ToLower(strings.TrimSpace(claim.ConflictKey))
		if key == "" || strings.TrimSpace(claim.ConflictValue) == "" {
			continue
		}
		if _, exists := groups[key]; !exists {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], index)
	}
	conflicts := []ClaimConflict{}
	for _, key := range keys {
		indexes := groups[key]
		values := []string{}
		for _, index := range indexes {
			values = appendUniqueStable(values, claims[index].ConflictValue)
		}
		if len(values) < 2 {
			continue
		}
		claimIDs := make([]string, 0, len(indexes))
		for _, index := range indexes {
			claims[index].Status = ClaimConflicted
			claimIDs = append(claimIDs, claims[index].ID)
		}
		conflicts = append(conflicts, ClaimConflict{ConflictKey: key, ClaimIDs: claimIDs, Values: values, ReasonCode: "exclusive_values_disagree"})
	}
	return conflicts
}

func buildCitations(claims []SynthesizedClaim, evidence evidenceCollection) ([]SynthesizedCitation, []SharedEvidence) {
	referenced := make(map[string]struct{})
	for _, claim := range claims {
		for _, reference := range claim.EvidenceRefs {
			referenced[reference] = struct{}{}
		}
	}
	citations := []SynthesizedCitation{}
	items := []SharedEvidence{}
	for _, evidenceID := range evidence.order {
		if _, exists := referenced[evidenceID]; !exists {
			continue
		}
		item, exists := evidence.items[evidenceID]
		if !exists {
			continue
		}
		citationID := fmt.Sprintf("C%d", len(citations)+1)
		citations = append(citations, SynthesizedCitation{
			CitationID: citationID, EvidenceID: item.ID, SourceType: item.SourceType,
			SourceID: item.SourceID, SourceVersion: item.SourceVersion, LineStart: item.LineStart, LineEnd: item.LineEnd,
		})
		items = append(items, item)
	}
	return citations, items
}

func assignCitationIDs(claims []SynthesizedClaim, citations []SynthesizedCitation) {
	byEvidence := make(map[string]string, len(citations))
	for _, citation := range citations {
		byEvidence[citation.EvidenceID] = citation.CitationID
	}
	for index := range claims {
		claims[index].CitationIDs = []string{}
		for _, reference := range claims[index].EvidenceRefs {
			if citationID := byEvidence[reference]; citationID != "" {
				claims[index].CitationIDs = append(claims[index].CitationIDs, citationID)
			}
		}
	}
}

func failedAgents(tasks []TaskExecution) []string {
	result := []string{}
	for _, task := range tasks {
		if task.Status != TaskStatusSucceeded {
			result = appendUniqueSorted(result, task.Agent)
		}
	}
	return result
}

func synthesisOutcome(claims []SynthesizedClaim, conflicts []ClaimConflict, degraded []string) (string, string, string) {
	switch {
	case len(claims) == 0:
		return SynthesisInsufficient, "no_supported_claims", "diagnosis_standard"
	case len(conflicts) > 0:
		return SynthesisConflict, "evidence_conflict_requires_user_review", "diagnosis_standard"
	case len(degraded) > 0:
		return SynthesisPartial, "partial_agent_results", "diagnosis_standard"
	default:
		return SynthesisComplete, "all_claims_citation_verified", ""
	}
}

func compileUnifiedAnswer(status string, claims []SynthesizedClaim) string {
	if len(claims) == 0 {
		return "当前没有通过引用校验的结论，已回退标准诊断。"
	}
	prefix := "综合两个受限 Agent 的结构化结果，得到以下已验证结论："
	if status == SynthesisConflict {
		prefix = "检测到有效证据之间存在冲突，系统不会静默选边："
	} else if status == SynthesisPartial {
		prefix = "部分 Agent 已降级，仅保留通过引用校验的结论："
	}
	var builder strings.Builder
	builder.WriteString(prefix)
	for index, claim := range claims {
		fmt.Fprintf(&builder, "\n%d. %s", index+1, claim.Statement)
		if len(claim.CitationIDs) > 0 {
			builder.WriteString(" [")
			builder.WriteString(strings.Join(claim.CitationIDs, ","))
			builder.WriteString("]")
		}
		if claim.Status == ClaimConflicted {
			builder.WriteString("（冲突候选）")
		}
	}
	return builder.String()
}

func appendUniqueStable(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func appendUniqueSorted(values []string, addition string) []string {
	values = appendUniqueStable(values, addition)
	sort.Strings(values)
	return values
}
