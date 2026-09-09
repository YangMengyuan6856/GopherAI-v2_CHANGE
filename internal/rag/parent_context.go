package rag

import (
	"context"
	"errors"
	"strings"
)

const (
	ParentContextStrategyName    = "rag_parent_context"
	ParentContextStrategyVersion = "rag-parent-context-v1"
	maxChildrenPerParent         = 2
	maxChildrenPerDocument       = 3
)

type ParentContextDiagnostics struct {
	Version            string `json:"version"`
	CandidatesBefore   int    `json:"candidates_before"`
	CandidatesAfter    int    `json:"candidates_after"`
	ParentContextHits  int    `json:"parent_context_hits"`
	FilteredByParent   int    `json:"filtered_by_parent"`
	FilteredByDocument int    `json:"filtered_by_document"`
	ChildCitationOnly  bool   `json:"child_citation_only"`
}

type ParentContextRetriever struct {
	base Searcher
}

func NewParentContextRetriever(base Searcher) (*ParentContextRetriever, error) {
	if base == nil {
		return nil, errors.New("base retriever is required")
	}
	return &ParentContextRetriever{base: base}, nil
}

func (retriever *ParentContextRetriever) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	desiredTopK := input.TopK
	if desiredTopK == 0 {
		desiredTopK = DefaultTopK
	}
	if desiredTopK < 1 || desiredTopK > MaxTopK {
		return SearchOutput{}, ErrInvalidSearch
	}
	baseInput := input
	baseInput.TopK = MaxTopK
	output, err := retriever.base.Search(ctx, baseInput)
	if err != nil {
		return output, err
	}
	diagnostics := &ParentContextDiagnostics{
		Version: ParentContextStrategyVersion, CandidatesBefore: len(output.Hits), ChildCitationOnly: true,
	}
	parentOccupancy := make(map[string]int)
	documentOccupancy := make(map[string]int)
	selected := make([]SearchHit, 0, min(desiredTopK, len(output.Hits)))
	for _, hit := range output.Hits {
		evidence := hit.Evidence
		documentID := strings.TrimSpace(evidence.SourceID)
		if documentID != "" && documentOccupancy[documentID] >= maxChildrenPerDocument {
			diagnostics.FilteredByDocument++
			continue
		}
		parentID := strings.TrimSpace(evidence.ParentEvidenceID)
		if parentID != "" && parentOccupancy[parentID] >= maxChildrenPerParent {
			diagnostics.FilteredByParent++
			continue
		}
		selected = append(selected, hit)
		if documentID != "" {
			documentOccupancy[documentID]++
		}
		if parentID != "" {
			parentOccupancy[parentID]++
		}
		if strings.TrimSpace(evidence.ParentContext) != "" {
			diagnostics.ParentContextHits++
		}
		if len(selected) == desiredTopK {
			break
		}
	}
	diagnostics.CandidatesAfter = len(selected)
	output.Hits = selected
	output.Diagnostics.FusedCandidates = len(selected)
	output.Conflicts = DetectEvidenceConflicts(selected)
	output.Diagnostics.ConflictVersion = EvidenceConflictVersion
	output.Diagnostics.ValidConflicts = len(output.Conflicts)
	output.Diagnostics.Parent = diagnostics
	output.Diagnostics.QueryAssessment = AssessQuery(input.Query, selected, output.Diagnostics)
	return output, nil
}
