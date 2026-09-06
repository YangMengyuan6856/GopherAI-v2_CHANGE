package evolution

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"GopherAI/internal/evaluation"
)

const (
	SplitSchemaVersion  = "harness-evolution-split-v1"
	SplitPolicyVersion  = "diagnostic-stable-rank-20-10-10-v1"
	SplitDatasetVersion = "devsupport-diagnostic-v1"
	SplitTotalCases     = 40
	EvolutionCaseCount  = 20
	ValidationCaseCount = 10
	HoldoutCaseCount    = 10

	StageCandidateSearch = "candidate_search"
	StageValidation      = "validation_evaluation"
	StageFinalEvaluation = "final_evaluation"
	SplitEvolution       = "evolution"
	SplitValidation      = "validation"
	SplitSealedHoldout   = "sealed_holdout"
)

var (
	ErrSplitAccessDenied       = errors.New("dataset split access denied")
	ErrNewExperimentRequired   = errors.New("sealed holdout was already opened; a new experiment version is required")
	ErrEvolutionDatasetInvalid = errors.New("harness evolution dataset is invalid")
)

type SplitDescriptor struct {
	Name                    string `json:"name"`
	Purpose                 string `json:"purpose"`
	CaseCount               int    `json:"case_count"`
	CaseSetSHA256           string `json:"case_set_sha256"`
	CandidateSearchReadable bool   `json:"candidate_search_readable"`
	CaseIDsExposed          bool   `json:"case_ids_exposed"`
	SealState               string `json:"seal_state"`
}

type SplitAudit struct {
	SchemaVersion                      string            `json:"schema_version"`
	PolicyVersion                      string            `json:"policy_version"`
	DatasetVersion                     string            `json:"dataset_version"`
	SourceSHA256                       string            `json:"source_sha256"`
	SourceHashVerified                 bool              `json:"source_hash_verified"`
	TotalCases                         int               `json:"total_cases"`
	CoveredCases                       int               `json:"covered_cases"`
	OverlapCount                       int               `json:"overlap_count"`
	DuplicateIDCount                   int               `json:"duplicate_id_count"`
	Splits                             []SplitDescriptor `json:"splits"`
	CandidateSearchAccessibleSplits    []string          `json:"candidate_search_accessible_splits"`
	HoldoutOpenCount                   int               `json:"holdout_open_count"`
	HoldoutOpenAPIAvailable            bool              `json:"holdout_open_api_available"`
	ReopenRequiresNewExperimentVersion bool              `json:"reopen_requires_new_experiment_version"`
	Guardrails                         []string          `json:"guardrails"`
	Limitations                        []string          `json:"limitations"`
}

type SplitAcceptanceCase struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	ReasonCode string `json:"reason_code"`
}

type SplitAcceptance struct {
	SchemaVersion string                `json:"schema_version"`
	Passed        bool                  `json:"passed"`
	Cases         []SplitAcceptanceCase `json:"cases"`
	Audit         SplitAudit            `json:"audit"`
}

type splitCaseHeader struct {
	ID             string `json:"id"`
	DatasetVersion string `json:"dataset_version"`
}

type splitMember struct {
	id   string
	rank string
}

func LoadSplitAudit(datasetPath, catalogManifestPath string) (SplitAudit, error) {
	catalog, err := evaluation.ValidateEvalCatalogFile(catalogManifestPath)
	if err != nil || !catalog.Passed {
		return SplitAudit{}, fmt.Errorf("%w: evaluation catalog did not validate", ErrEvolutionDatasetInvalid)
	}
	var sourceSHA string
	for _, slice := range catalog.Slices {
		if slice.Name == "diagnosis" && slice.ActualCount == SplitTotalCases && slice.Passed {
			sourceSHA = slice.ActualSHA
			break
		}
	}
	if sourceSHA == "" {
		return SplitAudit{}, fmt.Errorf("%w: diagnosis slice is missing", ErrEvolutionDatasetInvalid)
	}
	encoded, err := os.ReadFile(datasetPath)
	if err != nil {
		return SplitAudit{}, err
	}
	digest := sha256.Sum256(encoded)
	actualSHA := hex.EncodeToString(digest[:])
	if actualSHA != sourceSHA {
		return SplitAudit{}, fmt.Errorf("%w: source sha mismatch", ErrEvolutionDatasetInvalid)
	}
	ids, duplicateCount, err := decodeSplitCaseIDs(encoded)
	if err != nil {
		return SplitAudit{}, err
	}
	return BuildSplitAudit(ids, actualSHA, duplicateCount)
}

func BuildSplitAudit(ids []string, sourceSHA string, duplicateCount int) (SplitAudit, error) {
	if len(ids) != SplitTotalCases || duplicateCount != 0 || len(sourceSHA) != 64 {
		return SplitAudit{}, fmt.Errorf("%w: count, uniqueness or source hash mismatch", ErrEvolutionDatasetInvalid)
	}
	members := make([]splitMember, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return SplitAudit{}, fmt.Errorf("%w: empty case id", ErrEvolutionDatasetInvalid)
		}
		if _, duplicate := seen[id]; duplicate {
			return SplitAudit{}, fmt.Errorf("%w: duplicate case id", ErrEvolutionDatasetInvalid)
		}
		seen[id] = struct{}{}
		members = append(members, splitMember{id: id, rank: digestString(SplitPolicyVersion + "\x00" + id)})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].rank == members[j].rank {
			return members[i].id < members[j].id
		}
		return members[i].rank < members[j].rank
	})
	evolutionIDs := memberIDs(members[:EvolutionCaseCount])
	validationIDs := memberIDs(members[EvolutionCaseCount : EvolutionCaseCount+ValidationCaseCount])
	holdoutIDs := memberIDs(members[EvolutionCaseCount+ValidationCaseCount:])
	overlap := overlapCount(evolutionIDs, validationIDs, holdoutIDs)
	if overlap != 0 || len(evolutionIDs)+len(validationIDs)+len(holdoutIDs) != len(ids) {
		return SplitAudit{}, fmt.Errorf("%w: split overlap or coverage mismatch", ErrEvolutionDatasetInvalid)
	}
	return SplitAudit{
		SchemaVersion: SplitSchemaVersion, PolicyVersion: SplitPolicyVersion, DatasetVersion: SplitDatasetVersion,
		SourceSHA256: sourceSHA, SourceHashVerified: true, TotalCases: len(ids), CoveredCases: len(ids),
		OverlapCount: overlap, DuplicateIDCount: duplicateCount,
		Splits: []SplitDescriptor{
			{Name: SplitEvolution, Purpose: "candidate_search_and_feedback", CaseCount: len(evolutionIDs), CaseSetSHA256: caseSetHash(evolutionIDs), CandidateSearchReadable: true, CaseIDsExposed: false, SealState: "frozen"},
			{Name: SplitValidation, Purpose: "post_freeze_model_selection", CaseCount: len(validationIDs), CaseSetSHA256: caseSetHash(validationIDs), CandidateSearchReadable: false, CaseIDsExposed: false, SealState: "frozen"},
			{Name: SplitSealedHoldout, Purpose: "one_time_final_generalization_check", CaseCount: len(holdoutIDs), CaseSetSHA256: caseSetHash(holdoutIDs), CandidateSearchReadable: false, CaseIDsExposed: false, SealState: "sealed"},
		},
		CandidateSearchAccessibleSplits: []string{SplitEvolution}, HoldoutOpenCount: 0, HoldoutOpenAPIAvailable: false,
		ReopenRequiresNewExperimentVersion: true,
		Guardrails:                         []string{"source_catalog_sha_verified", "stable_hash_partition", "zero_overlap", "full_coverage", "candidate_search_evolution_only", "validation_after_candidate_freeze", "holdout_final_evaluation_only", "case_ids_not_exposed"},
		Limitations:                        []string{"当前只冻结并审计分区；M9-23 才会运行公平预算 A/B。", "Full 320 人工标签尚未复核，因此任何后续结果仍不得直接晋级。"},
	}, nil
}

func CheckSplitAccess(stage, split string, candidateFrozen, holdoutAlreadyOpened bool) error {
	switch {
	case stage == StageCandidateSearch && split == SplitEvolution:
		return nil
	case stage == StageValidation && split == SplitValidation && candidateFrozen:
		return nil
	case stage == StageFinalEvaluation && split == SplitSealedHoldout && candidateFrozen && !holdoutAlreadyOpened:
		return nil
	case stage == StageFinalEvaluation && split == SplitSealedHoldout && holdoutAlreadyOpened:
		return ErrNewExperimentRequired
	default:
		return ErrSplitAccessDenied
	}
}

func RunSplitAcceptance(datasetPath, catalogManifestPath string) (SplitAcceptance, error) {
	audit, err := LoadSplitAudit(datasetPath, catalogManifestPath)
	if err != nil {
		return SplitAcceptance{}, err
	}
	cases := []SplitAcceptanceCase{
		{Name: "source catalog hash is verified", Passed: audit.SourceHashVerified, ReasonCode: "source_hash_verified"},
		{Name: "all cases are uniquely covered", Passed: audit.CoveredCases == audit.TotalCases && audit.DuplicateIDCount == 0, ReasonCode: "unique_full_coverage"},
		{Name: "split overlap is zero", Passed: audit.OverlapCount == 0, ReasonCode: "zero_overlap"},
		{Name: "candidate search can read evolution", Passed: CheckSplitAccess(StageCandidateSearch, SplitEvolution, false, false) == nil, ReasonCode: "evolution_search_allowed"},
		{Name: "candidate search cannot read validation", Passed: errors.Is(CheckSplitAccess(StageCandidateSearch, SplitValidation, false, false), ErrSplitAccessDenied), ReasonCode: "validation_search_denied"},
		{Name: "candidate search cannot read holdout", Passed: errors.Is(CheckSplitAccess(StageCandidateSearch, SplitSealedHoldout, false, false), ErrSplitAccessDenied), ReasonCode: "holdout_search_denied"},
		{Name: "validation requires frozen candidate", Passed: errors.Is(CheckSplitAccess(StageValidation, SplitValidation, false, false), ErrSplitAccessDenied) && CheckSplitAccess(StageValidation, SplitValidation, true, false) == nil, ReasonCode: "candidate_freeze_required"},
		{Name: "holdout re-open requires new experiment", Passed: errors.Is(CheckSplitAccess(StageFinalEvaluation, SplitSealedHoldout, true, true), ErrNewExperimentRequired), ReasonCode: "new_experiment_required"},
	}
	passed := true
	for _, item := range cases {
		passed = passed && item.Passed
	}
	return SplitAcceptance{SchemaVersion: SplitSchemaVersion, Passed: passed, Cases: cases, Audit: audit}, nil
}

func decodeSplitCaseIDs(encoded []byte) ([]string, int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	ids := make([]string, 0, SplitTotalCases)
	seen := make(map[string]struct{}, SplitTotalCases)
	duplicates := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		var header splitCaseHeader
		if err := decoder.Decode(&header); err != nil {
			return nil, duplicates, fmt.Errorf("%w: invalid case json", ErrEvolutionDatasetInvalid)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || header.DatasetVersion != SplitDatasetVersion || strings.TrimSpace(header.ID) == "" {
			return nil, duplicates, fmt.Errorf("%w: invalid case header", ErrEvolutionDatasetInvalid)
		}
		if _, duplicate := seen[header.ID]; duplicate {
			duplicates++
		}
		seen[header.ID] = struct{}{}
		ids = append(ids, header.ID)
	}
	if err := scanner.Err(); err != nil {
		return nil, duplicates, err
	}
	return ids, duplicates, nil
}

func memberIDs(members []splitMember) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.id)
	}
	return ids
}

func caseSetHash(ids []string) string {
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	return digestString(strings.Join(ordered, "\n"))
}

func overlapCount(groups ...[]string) int {
	seen := make(map[string]int)
	overlap := 0
	for _, group := range groups {
		for _, id := range group {
			seen[id]++
			if seen[id] == 2 {
				overlap++
			}
		}
	}
	return overlap
}
