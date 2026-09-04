package intent

import (
	"GopherAI/internal/contract"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/embedding"
)

const PrototypeVersion = "intent-prototype-v1"

var (
	ErrPrototypeUnavailable = errors.New("intent prototype recognizer unavailable")
	ErrInvalidEmbedding     = errors.New("invalid intent embedding")
)

type Prototype struct {
	Intent string
	Text   string
}

type PrototypeConfig struct {
	Version        string
	Prototypes     []Prototype
	Threshold      float64
	MinimumMargin  float64
	BatchSize      int
	Timeout        time.Duration
	FailureBackoff time.Duration
}

type PrototypeScore struct {
	Intent string  `json:"intent"`
	Score  float64 `json:"score"`
}

type PrototypeDecision struct {
	Result  contract.IntentResult `json:"result"`
	Matched bool                  `json:"matched"`
	Margin  float64               `json:"margin"`
	Scores  []PrototypeScore      `json:"scores"`
}

type PrototypeRecognizer struct {
	embedder embedding.Embedder
	config   PrototypeConfig

	mu          sync.Mutex
	centroids   map[string][]float64
	lastFailure time.Time
	lastError   error
}

func DefaultPrototypeConfig() PrototypeConfig {
	return PrototypeConfig{
		Version: PrototypeVersion, Threshold: 0.85, MinimumMargin: 0.10,
		BatchSize: 10, Timeout: 2500 * time.Millisecond, FailureBackoff: 30 * time.Second,
		Prototypes: []Prototype{
			{ProjectQA, "根据项目文档回答配置默认值和支持能力"},
			{ProjectQA, "从代码和部署手册中查找事实并给出证据"},
			{ProjectQA, "比较两个项目版本或策略在资料中的差异"},
			{ProjectQA, "知识库里对这个架构设计是怎么说明的"},
			{Troubleshooting, "服务启动失败，请根据报错定位根因"},
			{Troubleshooting, "页面行为不符合预期，给出排查和验证步骤"},
			{Troubleshooting, "日志异常但依赖看起来正常，如何缩小故障范围"},
			{Troubleshooting, "请求变慢或反复重试，分析可能原因"},
			{DocTask, "上传一份项目文档到知识库并等待索引"},
			{DocTask, "删除或安全重建选中的知识文档"},
			{DocTask, "为现有文档创建新版本并查看索引状态"},
			{DocTask, "管理知识库文档的生命周期"},
			{ToolTask, "调用只读工具检查线上服务健康状态"},
			{ToolTask, "读取监控指标、告警和当前部署状态"},
			{ToolTask, "执行受治理的外部操作并遵守权限限制"},
			{ToolTask, "查询容器、队列或运行时的实时状态"},
			{FollowUp, "那第二种原因怎么验证"},
			{FollowUp, "这个结果为什么会这样"},
			{FollowUp, "继续说明刚才的步骤"},
			{FollowUp, "上面提到的对象接下来怎么处理"},
			{General, "你好，介绍一下你自己"},
			{General, "解释一个通用计算机概念"},
			{General, "帮我润色、翻译或制定学习计划"},
			{General, "陪我聊天或者讲一个笑话"},
		},
	}
}

func NewPrototypeRecognizer(embedder embedding.Embedder, config PrototypeConfig) (*PrototypeRecognizer, error) {
	if embedder == nil {
		return nil, fmt.Errorf("%w: embedder is required", ErrPrototypeUnavailable)
	}
	if strings.TrimSpace(config.Version) == "" || len(config.Prototypes) == 0 {
		return nil, fmt.Errorf("%w: version and prototypes are required", ErrPrototypeUnavailable)
	}
	if config.Threshold <= 0 || config.Threshold > 1 || config.MinimumMargin < 0 || config.MinimumMargin > 1 {
		return nil, fmt.Errorf("%w: invalid threshold or margin", ErrPrototypeUnavailable)
	}
	if config.BatchSize < 1 || config.BatchSize > 10 || config.Timeout <= 0 || config.FailureBackoff < 0 {
		return nil, fmt.Errorf("%w: invalid batch, timeout, or backoff", ErrPrototypeUnavailable)
	}
	seen := make(map[string]bool, len(Labels()))
	for _, prototype := range config.Prototypes {
		if !IsKnown(prototype.Intent) || strings.TrimSpace(prototype.Text) == "" {
			return nil, fmt.Errorf("%w: invalid prototype", ErrPrototypeUnavailable)
		}
		seen[prototype.Intent] = true
	}
	for _, label := range Labels() {
		if !seen[label] {
			return nil, fmt.Errorf("%w: missing label %s", ErrPrototypeUnavailable, label)
		}
	}
	return &PrototypeRecognizer{embedder: embedder, config: config}, nil
}

func (recognizer *PrototypeRecognizer) Recognize(ctx context.Context, question string) (PrototypeDecision, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return PrototypeDecision{}, fmt.Errorf("%w: question is required", ErrPrototypeUnavailable)
	}
	centroids, err := recognizer.prototypeCentroids(ctx)
	if err != nil {
		return PrototypeDecision{}, err
	}
	callContext, cancel := context.WithTimeout(ctx, recognizer.config.Timeout)
	defer cancel()
	vectors, err := recognizer.embedder.EmbedStrings(callContext, []string{question})
	if err != nil {
		return PrototypeDecision{}, fmt.Errorf("%w: query embedding: %v", ErrPrototypeUnavailable, err)
	}
	if len(vectors) != 1 {
		return PrototypeDecision{}, fmt.Errorf("%w: query vector count", ErrInvalidEmbedding)
	}
	query, err := normalizedVector(vectors[0])
	if err != nil {
		return PrototypeDecision{}, err
	}
	scores := make([]PrototypeScore, 0, len(centroids))
	for label, centroid := range centroids {
		if len(centroid) != len(query) {
			return PrototypeDecision{}, fmt.Errorf("%w: dimension mismatch", ErrInvalidEmbedding)
		}
		scores = append(scores, PrototypeScore{Intent: label, Score: dot(query, centroid)})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			return scores[i].Intent < scores[j].Intent
		}
		return scores[i].Score > scores[j].Score
	})
	margin := scores[0].Score
	if len(scores) > 1 {
		margin -= scores[1].Score
	}
	matched := scores[0].Score >= recognizer.config.Threshold && margin >= recognizer.config.MinimumMargin
	reason := "prototype_high_confidence"
	if scores[0].Score < recognizer.config.Threshold {
		reason = "prototype_low_score"
	} else if margin < recognizer.config.MinimumMargin {
		reason = "prototype_low_margin"
	}
	result := contract.IntentResult{
		Intent: scores[0].Intent, Confidence: clampSimilarity(scores[0].Score), Version: recognizer.config.Version,
		Stages: []contract.IntentStageResult{{Stage: "prototype", Intent: scores[0].Intent, Confidence: clampSimilarity(scores[0].Score), ReasonCode: reason}},
	}
	return PrototypeDecision{Result: result, Matched: matched, Margin: margin, Scores: boundedScores(scores, 3)}, nil
}

func (recognizer *PrototypeRecognizer) prototypeCentroids(ctx context.Context) (map[string][]float64, error) {
	recognizer.mu.Lock()
	defer recognizer.mu.Unlock()
	if recognizer.centroids != nil {
		return recognizer.centroids, nil
	}
	if recognizer.lastError != nil && time.Since(recognizer.lastFailure) < recognizer.config.FailureBackoff {
		return nil, fmt.Errorf("%w: prototype cache backoff: %v", ErrPrototypeUnavailable, recognizer.lastError)
	}
	texts := make([]string, len(recognizer.config.Prototypes))
	for index, prototype := range recognizer.config.Prototypes {
		texts[index] = prototype.Text
	}
	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += recognizer.config.BatchSize {
		end := start + recognizer.config.BatchSize
		if end > len(texts) {
			end = len(texts)
		}
		callContext, cancel := context.WithTimeout(ctx, recognizer.config.Timeout)
		batch, err := recognizer.embedder.EmbedStrings(callContext, texts[start:end])
		cancel()
		if err != nil || len(batch) != end-start {
			if err == nil {
				err = fmt.Errorf("vector count mismatch")
			}
			recognizer.lastFailure, recognizer.lastError = time.Now(), err
			return nil, fmt.Errorf("%w: prototype embedding: %v", ErrPrototypeUnavailable, err)
		}
		vectors = append(vectors, batch...)
	}
	grouped := make(map[string][][]float64, len(Labels()))
	for index, vector := range vectors {
		normalized, err := normalizedVector(vector)
		if err != nil {
			recognizer.lastFailure, recognizer.lastError = time.Now(), err
			return nil, err
		}
		label := recognizer.config.Prototypes[index].Intent
		grouped[label] = append(grouped[label], normalized)
	}
	centroids := make(map[string][]float64, len(grouped))
	for label, group := range grouped {
		centroid, err := meanNormalized(group)
		if err != nil {
			recognizer.lastFailure, recognizer.lastError = time.Now(), err
			return nil, err
		}
		centroids[label] = centroid
	}
	recognizer.centroids = centroids
	recognizer.lastError = nil
	return recognizer.centroids, nil
}

func normalizedVector(vector []float64) ([]float64, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("%w: empty vector", ErrInvalidEmbedding)
	}
	result := make([]float64, len(vector))
	norm := 0.0
	for index, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: non-finite value", ErrInvalidEmbedding)
		}
		result[index] = value
		norm += value * value
	}
	if norm == 0 {
		return nil, fmt.Errorf("%w: zero vector", ErrInvalidEmbedding)
	}
	norm = math.Sqrt(norm)
	for index := range result {
		result[index] /= norm
	}
	return result, nil
}

func meanNormalized(vectors [][]float64) ([]float64, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("%w: empty prototype group", ErrInvalidEmbedding)
	}
	mean := make([]float64, len(vectors[0]))
	for _, vector := range vectors {
		if len(vector) != len(mean) {
			return nil, fmt.Errorf("%w: prototype dimension mismatch", ErrInvalidEmbedding)
		}
		for index, value := range vector {
			mean[index] += value
		}
	}
	return normalizedVector(mean)
}

func dot(left []float64, right []float64) float64 {
	result := 0.0
	for index := range left {
		result += left[index] * right[index]
	}
	return result
}

func clampSimilarity(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func boundedScores(scores []PrototypeScore, limit int) []PrototypeScore {
	if limit > len(scores) {
		limit = len(scores)
	}
	result := make([]PrototypeScore, limit)
	copy(result, scores[:limit])
	return result
}
