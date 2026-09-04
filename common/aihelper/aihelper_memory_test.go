package aihelper

import (
	"context"
	"errors"
	"testing"

	persistedmodel "GopherAI/model"

	"github.com/cloudwego/eino/schema"
)

type memoryTestModel struct {
	generateCalls int
}

func (model *memoryTestModel) GenerateResponse(context.Context, []*schema.Message) (*schema.Message, error) {
	model.generateCalls++
	return schema.AssistantMessage("answer", nil), nil
}

func (model *memoryTestModel) StreamResponse(context.Context, []*schema.Message, StreamCallback) (string, error) {
	return "answer", nil
}

func (*memoryTestModel) GetModelType() string { return "test" }

func TestGenerateStopsBeforeModelWhenDurableUserWriteFails(t *testing.T) {
	model := new(memoryTestModel)
	helper := NewAIHelper(model, "session-1")
	helper.SetContextSaveFunc(func(context.Context, *persistedmodel.Message) (*persistedmodel.Message, error) {
		return nil, errors.New("mysql unavailable")
	})
	if _, err := helper.GenerateResponse("alice", context.Background(), "question"); err == nil {
		t.Fatal("expected durable write error")
	}
	if model.generateCalls != 0 || len(helper.GetMessages()) != 0 {
		t.Fatalf("model ran before durable write: calls=%d messages=%d", model.generateCalls, len(helper.GetMessages()))
	}
}
