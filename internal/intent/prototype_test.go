package intent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
)

type fakePrototypeEmbedder struct {
	vectors map[string][]float64
	err     error
	calls   [][]string
}

func (embedder *fakePrototypeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	embedder.calls = append(embedder.calls, append([]string(nil), texts...))
	if embedder.err != nil {
		return nil, embedder.err
	}
	result := make([][]float64, len(texts))
	for index, text := range texts {
		result[index] = append([]float64(nil), embedder.vectors[text]...)
	}
	return result, nil
}

func smallPrototypeConfig() PrototypeConfig {
	return PrototypeConfig{
		Version: PrototypeVersion, Threshold: 0.85, MinimumMargin: 0.10, BatchSize: 2,
		Timeout: time.Second, FailureBackoff: time.Minute,
		Prototypes: []Prototype{
			{ProjectQA, "qa"}, {Troubleshooting, "trouble"}, {DocTask, "doc"},
			{ToolTask, "tool"}, {FollowUp, "follow"}, {General, "general"},
		},
	}
}

func TestPrototypeRecognizerUsesThresholdMarginBatchAndCache(t *testing.T) {
	embedder := &fakePrototypeEmbedder{vectors: map[string][]float64{
		"qa": {1, 0, 0, 0, 0, 0}, "trouble": {0, 1, 0, 0, 0, 0}, "doc": {0, 0, 1, 0, 0, 0},
		"tool": {0, 0, 0, 1, 0, 0}, "follow": {0, 0, 0, 0, 1, 0}, "general": {0, 0, 0, 0, 0, 1},
		"question": {0.99, 0.05, 0, 0, 0, 0}, "second": {0.98, 0.04, 0, 0, 0, 0},
	}}
	recognizer, err := NewPrototypeRecognizer(embedder, smallPrototypeConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := recognizer.Recognize(context.Background(), "question")
	if err != nil || !decision.Matched || decision.Result.Intent != ProjectQA || decision.Margin < 0.90 {
		t.Fatalf("unexpected decision: %+v err=%v", decision, err)
	}
	if len(embedder.calls) != 4 { // three prototype batches plus one query
		t.Fatalf("unexpected first-call batches: %v", embedder.calls)
	}
	if _, err := recognizer.Recognize(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if len(embedder.calls) != 5 { // cached prototypes, query only
		t.Fatalf("prototype cache was not reused: %v", embedder.calls)
	}
}

func TestPrototypeRecognizerAbstainsOnLowMargin(t *testing.T) {
	embedder := &fakePrototypeEmbedder{vectors: map[string][]float64{
		"qa": {1, 0}, "trouble": {0.99, 0.01}, "doc": {0, 1}, "tool": {0, 1}, "follow": {0, 1}, "general": {0, 1}, "ambiguous": {1, 0},
	}}
	recognizer, err := NewPrototypeRecognizer(embedder, smallPrototypeConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := recognizer.Recognize(context.Background(), "ambiguous")
	if err != nil || decision.Matched || decision.Result.Stages[0].ReasonCode != "prototype_low_margin" {
		t.Fatalf("unexpected ambiguous decision: %+v err=%v", decision, err)
	}
}

func TestPrototypeRecognizerBacksOffAfterProviderFailure(t *testing.T) {
	embedder := &fakePrototypeEmbedder{err: errors.New("provider unavailable")}
	recognizer, err := NewPrototypeRecognizer(embedder, smallPrototypeConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recognizer.Recognize(context.Background(), "question"); !errors.Is(err, ErrPrototypeUnavailable) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if _, err := recognizer.Recognize(context.Background(), "question"); !errors.Is(err, ErrPrototypeUnavailable) {
		t.Fatalf("expected backoff error, got %v", err)
	}
	if len(embedder.calls) != 1 {
		t.Fatalf("failure backoff did not stop repeated provider calls: %d", len(embedder.calls))
	}
}

func TestPrototypeRecognizerRejectsInvalidVectors(t *testing.T) {
	embedder := &fakePrototypeEmbedder{vectors: map[string][]float64{"qa": {0, 0}}}
	recognizer, err := NewPrototypeRecognizer(embedder, smallPrototypeConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recognizer.Recognize(context.Background(), "question"); !errors.Is(err, ErrInvalidEmbedding) {
		t.Fatalf("expected invalid embedding, got %v", err)
	}
}
