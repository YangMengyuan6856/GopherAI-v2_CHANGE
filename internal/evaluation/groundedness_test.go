package evaluation

import (
	"testing"

	"GopherAI/internal/contract"
)

func TestScoreGroundednessCombinesDeterministicAndJudgeSignals(t *testing.T) {
	result, err := ScoreGroundedness(GroundednessInput{
		TenantID: "tenant", Question: "后端端口是什么？", Answer: "后端默认端口为 8888 [1]",
		Evidence:  []contract.Evidence{{ID: "e1", TenantID: "tenant", SourceID: "doc", Content: "后端默认端口为 8888。"}},
		Citations: []contract.Citation{{ID: "c1", EvidenceID: "e1"}}, ExpectedFacts: []string{"8888"},
		Judge: completedJudgeResult(1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.DeterministicGroundedness != 1 || result.CombinedGroundedness != 1 || result.ClaimCount != 1 {
		t.Fatalf("grounded answer should pass: %+v", result)
	}
	if err := ValidateGroundednessResult(result); err != nil {
		t.Fatal(err)
	}
}

func TestScoreGroundednessRejectsUnknownCitationACLAndForbiddenClaim(t *testing.T) {
	result, err := ScoreGroundedness(GroundednessInput{
		TenantID: "tenant", Question: "端口？", Answer: "端口是 9999 [1]",
		Evidence:  []contract.Evidence{{ID: "e1", TenantID: "other-tenant", SourceID: "doc", Content: "端口是 8888。"}},
		Citations: []contract.Citation{{ID: "c1", EvidenceID: "missing"}}, ForbiddenClaims: []string{"9999"},
		Judge: completedJudgeResult(1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || !contains(result.FailureReasons, "citation_existence_failed") || !contains(result.FailureReasons, "evidence_acl_failed") || !contains(result.FailureReasons, "forbidden_claim_present") {
		t.Fatalf("critical deterministic failures must block: %+v", result)
	}
}

func TestScoreGroundednessDoesNotScoreFailedJudgeAsNeutral(t *testing.T) {
	result, err := ScoreGroundedness(GroundednessInput{
		TenantID: "tenant", Question: "后端端口是什么？", Answer: "后端默认端口为 8888 [1]",
		Evidence:  []contract.Evidence{{ID: "e1", TenantID: "tenant", SourceID: "doc", Content: "后端默认端口为 8888。"}},
		Citations: []contract.Citation{{ID: "c1", EvidenceID: "e1"}},
		Judge:     JudgeResult{Status: JudgeStatusFailed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.Status != "incomplete" || !contains(result.FailureReasons, "judge_incomplete") || result.CombinedOverall != 0 {
		t.Fatalf("failed judge must keep case incomplete: %+v", result)
	}
}

func TestScoreGroundednessRequiresCitationForEachClaim(t *testing.T) {
	result, err := ScoreGroundedness(GroundednessInput{
		TenantID: "tenant", Question: "配置？", Answer: "后端默认端口为 8888 [1]\n重试次数为 6",
		Evidence:  []contract.Evidence{{ID: "e1", TenantID: "tenant", SourceID: "doc", Content: "后端默认端口为 8888。重试次数为 6。"}},
		Citations: []contract.Citation{{ID: "c1", EvidenceID: "e1"}},
		Judge:     completedJudgeResult(1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.ClaimCitationCoverage != .5 || !contains(result.FailureReasons, "claim_citation_coverage_below_0.90") {
		t.Fatalf("uncited claim must be visible: %+v", result)
	}
}

func completedJudgeResult(groundedness float64, safety float64) JudgeResult {
	scores := JudgeScores{Relevance: 1, Completeness: 1, Helpfulness: 1, Groundedness: groundedness, Safety: safety}
	return JudgeResult{Status: JudgeStatusComplete, Scores: scores, Overall: weightedJudgeOverall(scores)}
}
