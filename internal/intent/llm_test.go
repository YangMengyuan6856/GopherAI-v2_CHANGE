package intent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeIntentModel struct {
	response *schema.Message
	err      error
	input    []*schema.Message
}

func (fake *fakeIntentModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	fake.input = input
	return fake.response, fake.err
}

func TestStructuredLLMRecognizerAcceptsStrictBoundedOutput(t *testing.T) {
	fake := &fakeIntentModel{response: &schema.Message{Content: `{"intent":"troubleshooting","confidence":0.93,"entities":{"component":"redis"},"is_compound":false,"needs_clarify":false}`}}
	recognizer, err := NewStructuredLLMRecognizer(fake, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	decision := recognizer.Recognize(context.Background(), LLMInput{Question: "服务无法连接", Candidates: []PrototypeScore{{Intent: Troubleshooting, Score: 0.8}}})
	if decision.Status != LLMStatusCompleted || decision.Result.Intent != Troubleshooting || decision.Result.Confidence != 0.93 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if len(fake.input) != 2 || !strings.Contains(fake.input[0].Content, "不可信用户文本") || !strings.Contains(fake.input[1].Content, `"question":"服务无法连接"`) {
		t.Fatalf("unsafe or missing prompt envelope: %+v", fake.input)
	}
}

func TestStructuredLLMRecognizerRejectsMarkdownUnknownFieldsAndLowConfidence(t *testing.T) {
	tests := []struct {
		content string
		reason  string
	}{
		{"```json\n{\"intent\":\"general\",\"confidence\":0.9}\n```", LLMReasonInvalidOutput},
		{`{"intent":"general","confidence":0.9,"entities":{},"is_compound":false,"needs_clarify":false,"reason":"free text"}`, LLMReasonInvalidOutput},
		{`{"intent":"project_qa","confidence":0.59,"entities":{},"is_compound":false,"needs_clarify":false}`, LLMReasonLowConfidence},
	}
	for _, test := range tests {
		fake := &fakeIntentModel{response: &schema.Message{Content: test.content}}
		recognizer, _ := NewStructuredLLMRecognizer(fake, time.Second)
		decision := recognizer.Recognize(context.Background(), LLMInput{Question: "q"})
		if decision.Status != LLMStatusFallback || decision.OutcomeReason != test.reason || !decision.Result.NeedsClarify {
			t.Fatalf("unsafe output accepted: %+v", decision)
		}
		if test.reason == LLMReasonLowConfidence && decision.Result.Intent != ProjectQA {
			t.Fatalf("low-confidence candidate should be retained only for clarification: %+v", decision)
		}
	}
}

func TestStructuredLLMRecognizerSanitizesNonRoutingEntityShapeWithoutDiscardingIntent(t *testing.T) {
	for _, content := range []string{
		`{"intent":"project_qa","confidence":0.91,"entities":["deployment"],"is_compound":false,"needs_clarify":false}`,
		`{"intent":"project_qa","confidence":0.91,"entities":{"port":9090,"component":" backend "},"is_compound":false,"needs_clarify":false}`,
	} {
		fake := &fakeIntentModel{response: &schema.Message{Content: content}}
		recognizer, _ := NewStructuredLLMRecognizer(fake, time.Second)
		decision := recognizer.Recognize(context.Background(), LLMInput{Question: "部署端口是什么？"})
		if decision.Status != LLMStatusCompleted || decision.Result.Intent != ProjectQA || !decision.EntitiesSanitized {
			t.Fatalf("bounded entity metadata should not discard a valid route decision: %+v", decision)
		}
		if len(decision.Result.Entities) > maxIntentEntities {
			t.Fatalf("sanitized entities exceeded bound: %+v", decision.Result.Entities)
		}
	}
}

func TestStructuredLLMRecognizerFailsClosedToClarification(t *testing.T) {
	for _, modelError := range []error{errors.New("provider down"), context.DeadlineExceeded} {
		fake := &fakeIntentModel{err: modelError}
		recognizer, _ := NewStructuredLLMRecognizer(fake, time.Second)
		decision := recognizer.Recognize(context.Background(), LLMInput{Question: "q"})
		if decision.Status != LLMStatusFallback || decision.Result.Intent != General || !decision.Result.NeedsClarify {
			t.Fatalf("unsafe model fallback: %+v", decision)
		}
	}
}

func TestStructuredLLMRecognizerBoundsInputAndCandidates(t *testing.T) {
	fake := &fakeIntentModel{response: &schema.Message{Content: `{"intent":"general","confidence":0.9,"entities":{},"is_compound":false,"needs_clarify":false}`}}
	recognizer, _ := NewStructuredLLMRecognizer(fake, time.Second)
	decision := recognizer.Recognize(context.Background(), LLMInput{Question: strings.Repeat("问", maxIntentQuestionRunes+1)})
	if decision.OutcomeReason != LLMReasonInvalidOutput || len(fake.input) != 0 {
		t.Fatalf("oversized input reached model: %+v", decision)
	}
}
