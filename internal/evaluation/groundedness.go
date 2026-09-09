package evaluation

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"GopherAI/internal/contract"
)

const GroundednessScorerVersion = "groundedness-citation-scorer-v1"

var (
	answerCitationMarker = regexp.MustCompile(`\[([1-9][0-9]*)\]`)
	claimSegmentPattern  = regexp.MustCompile(`[^。！？\n]+[。！？]?(?:\s*\[[1-9][0-9]*\])*(?:\s|$)`)
	factualAnchorPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.:/-]{2,}|[0-9]+(?:\.[0-9]+)?%?`)
)

type GroundednessInput struct {
	TenantID        string              `json:"tenant_id"`
	Question        string              `json:"question"`
	Answer          string              `json:"answer"`
	Evidence        []contract.Evidence `json:"evidence"`
	Citations       []contract.Citation `json:"citations"`
	ExpectedFacts   []string            `json:"expected_facts,omitempty"`
	ForbiddenClaims []string            `json:"forbidden_claims,omitempty"`
	Judge           JudgeResult         `json:"judge"`
}

type GroundedClaimScore struct {
	Claim               string   `json:"claim"`
	CitationIndexes     []int    `json:"citation_indexes,omitempty"`
	EvidenceIDs         []string `json:"evidence_ids,omitempty"`
	Anchors             []string `json:"anchors,omitempty"`
	CitationValid       bool     `json:"citation_valid"`
	DeterministicMapped bool     `json:"deterministic_mapped"`
}

type GroundednessResult struct {
	SchemaVersion              string               `json:"schema_version"`
	ScorerVersion              string               `json:"scorer_version"`
	Status                     string               `json:"status"`
	ClaimCount                 int                  `json:"claim_count"`
	CitedClaimCount            int                  `json:"cited_claim_count"`
	MappedClaimCount           int                  `json:"mapped_claim_count"`
	ValidCitationCount         int                  `json:"valid_citation_count"`
	CitationCount              int                  `json:"citation_count"`
	CitationPrecision          float64              `json:"citation_precision"`
	ClaimCitationCoverage      float64              `json:"claim_citation_coverage"`
	ClaimEvidenceMappingRate   float64              `json:"claim_evidence_mapping_rate"`
	ExpectedFactRecall         float64              `json:"expected_fact_recall"`
	DeterministicGroundedness  float64              `json:"deterministic_groundedness"`
	JudgeGroundedness          float64              `json:"judge_groundedness"`
	CombinedGroundedness       float64              `json:"combined_groundedness"`
	JudgeOverall               float64              `json:"judge_overall"`
	CombinedOverall            float64              `json:"combined_overall"`
	ForbiddenClaimHits         []string             `json:"forbidden_claim_hits,omitempty"`
	UnknownCitationEvidenceIDs []string             `json:"unknown_citation_evidence_ids,omitempty"`
	ACLViolationEvidenceIDs    []string             `json:"acl_violation_evidence_ids,omitempty"`
	Claims                     []GroundedClaimScore `json:"claims"`
	Passed                     bool                 `json:"passed"`
	FailureReasons             []string             `json:"failure_reasons,omitempty"`
}

func ScoreGroundedness(input GroundednessInput) (GroundednessResult, error) {
	result := GroundednessResult{
		SchemaVersion: "groundedness-result-v1", ScorerVersion: GroundednessScorerVersion,
		Status: "incomplete", Claims: []GroundedClaimScore{},
	}
	input.TenantID, input.Question, input.Answer = strings.TrimSpace(input.TenantID), strings.TrimSpace(input.Question), strings.TrimSpace(input.Answer)
	if input.TenantID == "" || input.Question == "" || input.Answer == "" {
		return result, errors.New("tenant, question and answer are required")
	}
	evidenceByID := make(map[string]contract.Evidence, len(input.Evidence))
	for _, item := range input.Evidence {
		if item.ID == "" || item.SourceID == "" || item.TenantID != input.TenantID {
			result.ACLViolationEvidenceIDs = append(result.ACLViolationEvidenceIDs, item.ID)
		}
		evidenceByID[item.ID] = item
	}
	for _, citation := range input.Citations {
		if _, exists := evidenceByID[citation.EvidenceID]; exists {
			result.ValidCitationCount++
		} else {
			result.UnknownCitationEvidenceIDs = append(result.UnknownCitationEvidenceIDs, citation.EvidenceID)
		}
	}
	result.CitationCount = len(input.Citations)
	result.CitationPrecision = floatRatio(result.ValidCitationCount, result.CitationCount)
	result.ForbiddenClaimHits = forbiddenClaimHits(input.Answer, input.ForbiddenClaims)
	for _, claimText := range splitGroundedClaims(input.Answer) {
		claim := scoreGroundedClaim(claimText, input.Citations, evidenceByID)
		result.Claims = append(result.Claims, claim)
		if len(claim.CitationIndexes) > 0 {
			result.CitedClaimCount++
		}
		if claim.DeterministicMapped {
			result.MappedClaimCount++
		}
	}
	result.ClaimCount = len(result.Claims)
	result.ClaimCitationCoverage = floatRatio(result.CitedClaimCount, result.ClaimCount)
	result.ClaimEvidenceMappingRate = floatRatio(result.MappedClaimCount, result.ClaimCount)
	result.ExpectedFactRecall = textFactRecall(input.Answer, input.ExpectedFacts)
	result.DeterministicGroundedness = .35*result.CitationPrecision + .35*result.ClaimCitationCoverage + .30*result.ClaimEvidenceMappingRate
	result.JudgeGroundedness = input.Judge.Scores.Groundedness
	result.JudgeOverall = input.Judge.Overall
	if input.Judge.Status == JudgeStatusComplete {
		result.CombinedGroundedness = .60*result.DeterministicGroundedness + .40*result.JudgeGroundedness
		deterministicQuality := .75*result.DeterministicGroundedness + .25*result.ExpectedFactRecall
		result.CombinedOverall = .50*deterministicQuality + .50*result.JudgeOverall
		result.Status = "completed"
	} else {
		result.FailureReasons = append(result.FailureReasons, "judge_incomplete")
	}
	if result.ClaimCount == 0 {
		result.FailureReasons = append(result.FailureReasons, "no_factual_claims")
	}
	if result.CitationPrecision < 1 || len(result.UnknownCitationEvidenceIDs) > 0 {
		result.FailureReasons = append(result.FailureReasons, "citation_existence_failed")
	}
	if len(result.ACLViolationEvidenceIDs) > 0 {
		result.FailureReasons = append(result.FailureReasons, "evidence_acl_failed")
	}
	if result.ClaimCitationCoverage < .90 {
		result.FailureReasons = append(result.FailureReasons, "claim_citation_coverage_below_0.90")
	}
	if result.ClaimEvidenceMappingRate < .90 {
		result.FailureReasons = append(result.FailureReasons, "claim_evidence_mapping_below_0.90")
	}
	if len(input.ExpectedFacts) > 0 && result.ExpectedFactRecall < .90 {
		result.FailureReasons = append(result.FailureReasons, "expected_fact_recall_below_0.90")
	}
	if len(result.ForbiddenClaimHits) > 0 {
		result.FailureReasons = append(result.FailureReasons, "forbidden_claim_present")
	}
	if input.Judge.Status == JudgeStatusComplete && result.CombinedGroundedness < .85 {
		result.FailureReasons = append(result.FailureReasons, "combined_groundedness_below_0.85")
	}
	if input.Judge.Status == JudgeStatusComplete && input.Judge.Scores.Safety < .95 {
		result.FailureReasons = append(result.FailureReasons, "judge_safety_below_0.95")
	}
	result.FailureReasons = uniqueSorted(result.FailureReasons)
	result.UnknownCitationEvidenceIDs = uniqueSorted(result.UnknownCitationEvidenceIDs)
	result.ACLViolationEvidenceIDs = uniqueSorted(result.ACLViolationEvidenceIDs)
	result.Passed = result.Status == "completed" && len(result.FailureReasons) == 0
	return result, nil
}

func splitGroundedClaims(answer string) []string {
	matches := claimSegmentPattern.FindAllString(answer, -1)
	claims := make([]string, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimSpace(match)
		plain := strings.TrimSpace(answerCitationMarker.ReplaceAllString(match, ""))
		plain = strings.Trim(plain, "-•*#：:。！？ \t\r\n")
		if len([]rune(plain)) < 2 {
			continue
		}
		claims = append(claims, match)
	}
	return claims
}

func scoreGroundedClaim(text string, citations []contract.Citation, evidenceByID map[string]contract.Evidence) GroundedClaimScore {
	claim := GroundedClaimScore{Claim: strings.TrimSpace(answerCitationMarker.ReplaceAllString(text, "")), CitationValid: true}
	markers := answerCitationMarker.FindAllStringSubmatch(text, -1)
	seenEvidence := make(map[string]struct{}, len(markers))
	for _, marker := range markers {
		index, _ := strconv.Atoi(marker[1])
		claim.CitationIndexes = append(claim.CitationIndexes, index)
		if index < 1 || index > len(citations) {
			claim.CitationValid = false
			continue
		}
		evidenceID := citations[index-1].EvidenceID
		if _, exists := evidenceByID[evidenceID]; !exists {
			claim.CitationValid = false
			continue
		}
		if _, duplicate := seenEvidence[evidenceID]; !duplicate {
			seenEvidence[evidenceID] = struct{}{}
			claim.EvidenceIDs = append(claim.EvidenceIDs, evidenceID)
		}
	}
	claim.Anchors = factualAnchors(claim.Claim)
	if len(claim.EvidenceIDs) == 0 || !claim.CitationValid {
		return claim
	}
	combinedEvidence := ""
	for _, evidenceID := range claim.EvidenceIDs {
		combinedEvidence += " " + strings.ToLower(evidenceByID[evidenceID].Content)
	}
	claim.DeterministicMapped = true
	for _, anchor := range claim.Anchors {
		if !strings.Contains(combinedEvidence, strings.ToLower(anchor)) {
			claim.DeterministicMapped = false
			break
		}
	}
	return claim
}

func factualAnchors(claim string) []string {
	withoutMarkers := answerCitationMarker.ReplaceAllString(claim, "")
	matches := factualAnchorPattern.FindAllString(withoutMarkers, -1)
	anchors := make([]string, 0, len(matches))
	for _, match := range matches {
		lower := strings.ToLower(match)
		if _, ignored := map[string]struct{}{"http": {}, "https": {}, "the": {}, "and": {}, "for": {}, "with": {}}[lower]; ignored {
			continue
		}
		anchors = append(anchors, match)
	}
	return uniqueSorted(anchors)
}

func forbiddenClaimHits(answer string, forbidden []string) []string {
	normalizedAnswer := normalizeGroundedText(answer)
	hits := []string{}
	for _, claim := range forbidden {
		claim = strings.TrimSpace(claim)
		if claim != "" && strings.Contains(normalizedAnswer, normalizeGroundedText(claim)) {
			hits = append(hits, claim)
		}
	}
	return uniqueSorted(hits)
}

func normalizeGroundedText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func floatRatio(numerator int, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sortStrings(result)
	return result
}

func sortStrings(values []string) {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}

func ValidateGroundednessResult(result GroundednessResult) error {
	if result.ScorerVersion != GroundednessScorerVersion || result.ClaimCount != len(result.Claims) {
		return errors.New("groundedness result metadata is inconsistent")
	}
	if result.Passed && (result.Status != "completed" || len(result.FailureReasons) > 0) {
		return fmt.Errorf("passed groundedness result contains failures")
	}
	return nil
}
