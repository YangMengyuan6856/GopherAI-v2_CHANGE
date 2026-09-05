package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeRetriever struct {
	input  rag.SearchInput
	output rag.SearchOutput
	err    error
}

func (retriever *fakeRetriever) Search(_ context.Context, input rag.SearchInput) (rag.SearchOutput, error) {
	retriever.input = input
	return retriever.output, retriever.err
}

type fakeModel struct {
	response  *schema.Message
	responses []*schema.Message
	err       error
	calls     int
	input     []*schema.Message
}

func (chatModel *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	chatModel.calls++
	chatModel.input = input
	if len(chatModel.responses) >= chatModel.calls {
		return chatModel.responses[chatModel.calls-1], chatModel.err
	}
	return chatModel.response, chatModel.err
}

func strongEvidence() rag.SearchOutput {
	return rag.SearchOutput{
		Hits: []rag.SearchHit{{Evidence: contract.Evidence{
			ID: "chunk-1", Kind: "document_chunk", TenantID: "tenant-a", SourceID: "doc-1", SourceVersion: "1",
			Title: "project.md", Section: "Runtime", LineStart: 4, LineEnd: 5, Content: "默认重试次数为 7。", Score: 0.98, Retrieval: "dense+bm25", ContentHash: "hash-1",
		}}},
		Diagnostics: rag.SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1, FusedCandidates: 1},
	}
}

func TestAgentBuildsGroundedPromptAndVerifiedCitation(t *testing.T) {
	retriever := &fakeRetriever{output: strongEvidence()}
	chatModel := &fakeModel{response: &schema.Message{Content: `{"answer":"默认重试次数为 7。[E1]","citations":["E1"]}`}}
	agent, err := NewAgent(retriever, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if err != nil {
		t.Fatal(err)
	}
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "默认重试几次？"})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Result.Resolved || output.Result.Answer != "默认重试次数为 7。[1]" || len(output.Result.Citations) != 1 {
		t.Fatalf("unexpected grounded output: %+v", output)
	}
	if output.Answer.Status != AnswerStatusCompleted || output.Answer.ModelAttempts != 1 {
		t.Fatalf("answer diagnostics were not completed: %+v", output.Answer)
	}
	if retriever.input.TenantID != "tenant-a" || retriever.input.UserID != "user-a" {
		t.Fatalf("ACL was not propagated: %+v", retriever.input)
	}
	if chatModel.calls != 1 || len(chatModel.input) != 2 || !strings.Contains(chatModel.input[1].Content, "[E1]") {
		t.Fatalf("model did not receive bounded evidence pack: %+v", chatModel.input)
	}
}

func TestParentContextAgentAddsContextButKeepsChildCitationBoundary(t *testing.T) {
	search := strongEvidence()
	search.Hits[0].Evidence.ParentEvidenceID = "parent-1"
	search.Hits[0].Evidence.ParentSection = "Runtime"
	search.Hits[0].Evidence.ParentLineStart = 1
	search.Hits[0].Evidence.ParentLineEnd = 20
	search.Hits[0].Evidence.ParentContext = "Runtime 章节还描述了部署和连接池背景。"
	parentModel := &fakeModel{response: &schema.Message{Content: `{"answer":"默认重试次数为 7。[E1]","citations":["E1"]}`}}
	parentAgent, err := NewParentContextAgent(&fakeRetriever{output: search}, parentModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parentAgent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "默认重试几次？"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parentModel.input[0].Content, "引用仍指向 Child 行号") || !strings.Contains(parentModel.input[1].Content, "<parent_context") || !strings.Contains(parentModel.input[1].Content, "默认重试次数为 7") {
		t.Fatalf("parent prompt lost its exact-child safety boundary: %+v", parentModel.input)
	}

	fastModel := &fakeModel{response: &schema.Message{Content: `{"answer":"默认重试次数为 7。[E1]","citations":["E1"]}`}}
	fastAgent, _ := NewAgent(&fakeRetriever{output: search}, fastModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if _, err := fastAgent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "默认重试几次？"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fastModel.input[1].Content, "<parent_context") {
		t.Fatal("rag_fast must not silently consume the parent-context candidate")
	}
}

func TestAgentDoesNotCallModelWhenEvidenceGateRejects(t *testing.T) {
	search := strongEvidence()
	search.Hits[0].Evidence.Retrieval = "dense"
	search.Diagnostics.KeywordCandidates = 0
	chatModel := new(fakeModel)
	agent, _ := NewAgent(&fakeRetriever{output: search}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "未知配置是什么？"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Result.Resolved || !output.Result.NeedsUserInput || chatModel.calls != 0 || !strings.Contains(output.Result.Answer, "没有调用模型") {
		t.Fatalf("evidence rejection must be deterministic and model-free: output=%+v calls=%d", output, chatModel.calls)
	}
	if output.Answer.Status != AnswerStatusGateRejected || output.Answer.ModelAttempts != 0 {
		t.Fatalf("gate rejection diagnostics were not stable: %+v", output.Answer)
	}
}

func TestAgentReturnsStructuredConflictWithoutCallingModel(t *testing.T) {
	search := strongEvidence()
	search.Conflicts = []contract.EvidenceConflict{{
		ConflictID: "conflict-1", FactKey: "release > timeout_seconds", Status: rag.EvidenceConflictStatusReview,
		Values: []contract.EvidenceConflictValue{
			{Value: "47", EvidenceID: "e1", SourceID: "json", SourceRevision: "rev-a", Authority: 50},
			{Value: "60", EvidenceID: "e2", SourceID: "yaml", SourceRevision: "rev-b", Authority: 80},
		},
	}}
	chatModel := new(fakeModel)
	agent, _ := NewAgent(&fakeRetriever{output: search}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "超时时间是多少？"})
	if err != nil {
		t.Fatal(err)
	}
	if chatModel.calls != 0 || output.Result.Resolved || !output.Result.NeedsUserInput || len(output.Result.Conflicts) != 1 {
		t.Fatalf("conflict must stop model generation and remain structured: output=%+v calls=%d", output, chatModel.calls)
	}
	for _, expected := range []string{"47", "60", "不会", "revision"} {
		if !strings.Contains(output.Result.Answer, expected) {
			t.Fatalf("conflict answer did not preserve %q: %s", expected, output.Result.Answer)
		}
	}
}

func TestAgentRepairsUnverifiableModelOutputOnce(t *testing.T) {
	chatModel := &fakeModel{responses: []*schema.Message{
		{Content: `{"answer":"答案没有引用","citations":["E1"]}`},
		{Content: `{"answer":"默认重试次数为 7。[E1]","citations":["E1"]}`},
	}}
	agent, _ := NewAgent(&fakeRetriever{output: strongEvidence()}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if err != nil || !output.Result.Resolved || chatModel.calls != 2 || output.Result.Answer != "默认重试次数为 7。[1]" {
		t.Fatalf("expected one bounded repair, output=%+v calls=%d err=%v", output, chatModel.calls, err)
	}
	if !strings.Contains(chatModel.input[len(chatModel.input)-1].Content, "未通过机器校验") {
		t.Fatalf("repair instruction was not sent: %+v", chatModel.input)
	}
}

func TestAgentFallsBackToAuthorizedCitationsAfterRepairFails(t *testing.T) {
	chatModel := &fakeModel{response: &schema.Message{Content: `{"answer":"答案没有引用","citations":["E1"]}`}}
	agent, _ := NewAgent(&fakeRetriever{output: strongEvidence()}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if err != nil || output.Result.Resolved || !output.Result.NeedsUserInput || chatModel.calls != 2 || len(output.Result.Citations) != 1 || !strings.Contains(output.Result.Answer, "停止输出未经验证的结论") {
		t.Fatalf("expected safe cited fallback, output=%+v calls=%d err=%v", output, chatModel.calls, err)
	}
	if output.Answer.Status != AnswerStatusSafetyFallback || output.Answer.ReasonCode != AnswerReasonCitationFailed {
		t.Fatalf("safety fallback diagnostics were not stable: %+v", output.Answer)
	}
}

func TestAgentNormalizesCommonStructuredCitationVariants(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "markdown prose and inferred list", response: "结果如下：\n```json\n{\"answer\":\"默认重试次数为 7。【E1】\"}\n```"},
		{name: "numeric citations", response: `{"answer":"默认重试次数为 7。[1]","citations":[1]}`},
		{name: "inline markers override redundant list", response: `{"answer":"默认重试次数为 7。[E1]","citations":["E1","E9"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chatModel := &fakeModel{response: &schema.Message{Content: test.response}}
			agent, _ := NewAgent(&fakeRetriever{output: strongEvidence()}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
			output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
			if err != nil || !output.Result.Resolved || output.Result.Answer != "默认重试次数为 7。[1]" || chatModel.calls != 1 {
				t.Fatalf("expected verified normalized output, output=%+v calls=%d err=%v", output, chatModel.calls, err)
			}
		})
	}
}

func TestAgentIncludesDeepEnhancementUsageInStrategyUsage(t *testing.T) {
	search := strongEvidence()
	search.Diagnostics.Deep = &rag.DeepSearchDiagnostics{Usage: contract.ModelUsage{InputTokens: 120, OutputTokens: 30, CostMicros: 99}}
	chatModel := &fakeModel{response: &schema.Message{Content: `{"answer":"默认重试次数为 7。[E1]","citations":["E1"]}`}}
	agent, _ := NewAgent(&fakeRetriever{output: search}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	output, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Result.Usage.InputTokens != 120 || output.Result.Usage.OutputTokens != 30 || output.Result.Usage.CostMicros != 99 {
		t.Fatalf("deep enhancement usage was not included: %+v", output.Result.Usage)
	}
}
