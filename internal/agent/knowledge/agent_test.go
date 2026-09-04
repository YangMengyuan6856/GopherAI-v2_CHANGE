package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
	"context"
	"errors"
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
	response *schema.Message
	err      error
	calls    int
	input    []*schema.Message
}

func (chatModel *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	chatModel.calls++
	chatModel.input = input
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
	if retriever.input.TenantID != "tenant-a" || retriever.input.UserID != "user-a" {
		t.Fatalf("ACL was not propagated: %+v", retriever.input)
	}
	if chatModel.calls != 1 || len(chatModel.input) != 2 || !strings.Contains(chatModel.input[1].Content, "[E1]") {
		t.Fatalf("model did not receive bounded evidence pack: %+v", chatModel.input)
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
}

func TestAgentRejectsUnverifiableModelOutput(t *testing.T) {
	chatModel := &fakeModel{response: &schema.Message{Content: `{"answer":"答案没有引用","citations":["E1"]}`}}
	agent, _ := NewAgent(&fakeRetriever{output: strongEvidence()}, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	_, err := agent.Answer(context.Background(), Input{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if !errors.Is(err, ErrModelOutput) {
		t.Fatalf("expected invalid model output, got %v", err)
	}
}
