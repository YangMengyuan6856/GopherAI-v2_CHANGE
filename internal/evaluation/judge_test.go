package evaluation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/contract"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type judgeModelStub struct {
	responses []*schema.Message
	errors    []error
	wait      bool
	calls     int
	options   []int
	inputs    [][]*schema.Message
}

func (stub *judgeModelStub) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	stub.calls++
	stub.options = append(stub.options, len(options))
	stub.inputs = append(stub.inputs, input)
	if stub.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	index := stub.calls - 1
	var response *schema.Message
	if index < len(stub.responses) {
		response = stub.responses[index]
	}
	if index < len(stub.errors) {
		return response, stub.errors[index]
	}
	return response, nil
}

func TestLLMJudgeEmptyEvidencePromptRequiresEmptySupportedClaims(t *testing.T) {
	valid := `{"scores":{"relevance":1,"completeness":1,"helpfulness":1,"groundedness":1,"safety":1},"supported_claims":[],"unsupported_claims":[],"reason":"正确拒绝无证据问题。","confidence":1}`
	modelStub := &judgeModelStub{responses: []*schema.Message{{Content: "not-json"}, {Content: valid}}}
	judge, err := NewLLMJudge(modelStub, "judge-v1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := validJudgeInput()
	input.Evidence = nil
	if _, err := judge.Judge(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(modelStub.inputs) != 2 || len(modelStub.inputs[0]) != 2 || len(modelStub.inputs[1]) != 3 {
		t.Fatalf("unexpected retry messages: %+v", modelStub.inputs)
	}
	if !strings.Contains(modelStub.inputs[0][0].Content, "evidence 为空，supported_claims 必须是 []") || !strings.Contains(modelStub.inputs[1][2].Content, "supported_claims 必须是空数组") {
		t.Fatal("empty-evidence constraint must be present in the initial rubric and retry repair")
	}
}

func TestLLMJudgeRetriesInvalidJSONAndComputesServerOverall(t *testing.T) {
	valid := `{"scores":{"relevance":1,"completeness":0.75,"helpfulness":0.75,"groundedness":1,"safety":1},"supported_claims":[{"claim":"端口是8888","evidence_ids":["e1"]}],"unsupported_claims":[],"reason":"回答与证据一致。","confidence":0.9}`
	modelStub := &judgeModelStub{responses: []*schema.Message{{Content: "```json\n{}\n```"}, {Content: valid}}}
	judge, err := NewLLMJudge(modelStub, "independent-judge-v1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := judge.Judge(context.Background(), validJudgeInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != JudgeStatusComplete || result.Attempts != 2 || modelStub.calls != 2 || result.Overall != .25*1+.20*.75+.20*.75+.25*1+.10*1 {
		t.Fatalf("unexpected judge result: %+v calls=%d", result, modelStub.calls)
	}
	if len(modelStub.options) != 2 || modelStub.options[0] == 0 || modelStub.options[1] == 0 {
		t.Fatalf("temperature option must be supplied on every attempt: %v", modelStub.options)
	}
}

func TestLLMJudgeRejectsScoresOutsideHumanGradeAnchors(t *testing.T) {
	response := `{"scores":{"relevance":0.8,"completeness":1,"helpfulness":1,"groundedness":1,"safety":1},"supported_claims":[{"claim":"端口是8888","evidence_ids":["e1"]}],"unsupported_claims":[],"reason":"使用了连续分数。","confidence":0.9}`
	modelStub := &judgeModelStub{responses: []*schema.Message{{Content: response}, {Content: response}}}
	judge, _ := NewLLMJudge(modelStub, "judge-v1", time.Second)
	result, err := judge.Judge(context.Background(), validJudgeInput())
	if !errors.Is(err, ErrJudgeFailed) || result.ErrorCode != "judge_output_invalid" {
		t.Fatalf("non-anchor score must fail closed: %+v err=%v", result, err)
	}
}

func TestLLMJudgeFailsClosedAfterTwoInvalidOutputs(t *testing.T) {
	modelStub := &judgeModelStub{responses: []*schema.Message{{Content: `{"scores":{"relevance":1}}`}, {Content: `not-json`}}}
	judge, _ := NewLLMJudge(modelStub, "judge-v1", time.Second)
	result, err := judge.Judge(context.Background(), validJudgeInput())
	if !errors.Is(err, ErrJudgeFailed) || result.Status != JudgeStatusFailed || result.Attempts != JudgeMaxAttempts || result.ErrorCode != "judge_output_invalid" {
		t.Fatalf("judge failure must be explicit: result=%+v err=%v", result, err)
	}
	if result.Overall != 0 {
		t.Fatal("failed judge must not masquerade as a neutral passing score")
	}
}

func TestLLMJudgeRejectsUnknownEvidenceReference(t *testing.T) {
	response := `{"scores":{"relevance":1,"completeness":1,"helpfulness":1,"groundedness":1,"safety":1},"supported_claims":[{"claim":"bad","evidence_ids":["unknown"]}],"unsupported_claims":[],"reason":"引用不存在。","confidence":1}`
	modelStub := &judgeModelStub{responses: []*schema.Message{{Content: response}, {Content: response}}}
	judge, _ := NewLLMJudge(modelStub, "judge-v1", time.Second)
	result, err := judge.Judge(context.Background(), validJudgeInput())
	if !errors.Is(err, ErrJudgeFailed) || result.ErrorCode != "judge_output_invalid" {
		t.Fatalf("unknown evidence reference must fail closed: %+v err=%v", result, err)
	}
}

func TestLLMJudgeTimeoutIsRetriedAndReported(t *testing.T) {
	modelStub := &judgeModelStub{wait: true}
	judge, _ := NewLLMJudge(modelStub, "judge-v1", 5*time.Millisecond)
	result, err := judge.Judge(context.Background(), validJudgeInput())
	if !errors.Is(err, ErrJudgeFailed) || result.ErrorCode != "judge_timeout" || result.Attempts != 2 {
		t.Fatalf("timeout must stay visible: %+v err=%v", result, err)
	}
}

func validJudgeInput() JudgeInput {
	return JudgeInput{
		TaskType: "project_qa", Question: "后端端口是什么？", Answer: "后端端口是 8888。[1]",
		Evidence:      []contract.Evidence{{ID: "e1", TenantID: "tenant", SourceID: "doc", Content: "后端端口为 8888。"}},
		ExpectedFacts: []string{"8888"}, ForbiddenClaims: []string{"9999"},
	}
}
