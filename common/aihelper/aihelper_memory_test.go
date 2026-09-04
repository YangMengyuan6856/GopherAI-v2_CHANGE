package aihelper

import (
	"context"
	"errors"
	"strings"
	"testing"

	profiledomain "GopherAI/internal/profilememory"
	persistedmodel "GopherAI/model"

	"github.com/cloudwego/eino/schema"
)

type memoryTestModel struct {
	generateCalls int
	messages      []*schema.Message
}

func (model *memoryTestModel) GenerateResponse(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
	model.generateCalls++
	model.messages = append([]*schema.Message(nil), messages...)
	return schema.AssistantMessage("answer", nil), nil
}

type profileRecallStub struct{ response profiledomain.RecallResponse }

func (stub profileRecallStub) Recall(context.Context, string, string, string, int) (profiledomain.RecallResponse, error) {
	return stub.response, nil
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

func TestGenerateInjectsOnlyRecalledProfileAsSystemContext(t *testing.T) {
	t.Chdir("../..")
	model := new(memoryTestModel)
	helper := NewAIHelper(model, "session-1")
	helper.InitMemory("alice")
	helper.SetProfileRecall(profileRecallStub{response: profiledomain.RecallResponse{Status: "hit", Items: []profiledomain.PublicMemory{{Key: "redis_version", Value: "7.4", Confidence: 1, Status: profiledomain.StatusActive}}}}, nil)
	nextID := uint(0)
	helper.SetContextSaveFunc(func(_ context.Context, message *persistedmodel.Message) (*persistedmodel.Message, error) {
		nextID++
		message.ID = nextID
		return message, nil
	})
	if _, err := helper.GenerateResponse("alice", context.Background(), "我当前 Redis 版本是什么？"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range model.messages {
		if message.Role == schema.System && strings.Contains(message.Content, "confirmed_environment.redis_version=7.4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("active profile was not supplied to model: %#v", model.messages)
	}
}
