package collaborationplatform

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/config"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/incident"
	"GopherAI/internal/orchestration"
	knowledgeapp "GopherAI/internal/platform/knowledge"
	"GopherAI/internal/toolruntime"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
)

// Lazy construction avoids network/config startup dependencies. Model config is
// server-owned; the browser cannot override prompts, URLs, credentials or budgets.
type Runner struct{}

func NewDefaultRunner() *Runner { return &Runner{} }

func (*Runner) Run(ctx context.Context, input orchestration.ExecutionInput) (orchestration.CollaborationRun, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return orchestration.CollaborationRun{}, errors.New("collaboration model not configured")
	}
	cfg := config.GetConfig()
	name := strings.TrimSpace(os.Getenv("GOPHERAI_COLLABORATION_MODEL"))
	if name == "" {
		name = cfg.RagChatModelName
		if name == "qwen-turbo" && strings.Contains(cfg.RagBaseUrl, "dashscope") {
			name = "qwen-plus"
		}
	}
	temperature := float32(0)
	maxTokens := 2000
	m, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{BaseURL: cfg.RagBaseUrl, APIKey: key, Model: name, Temperature: &temperature, MaxTokens: &maxTokens, ResponseFormat: &modelOpenAI.ChatCompletionResponseFormat{Type: modelOpenAI.ChatCompletionResponseFormatTypeJSONObject}})
	if err != nil {
		return orchestration.CollaborationRun{}, errors.New("collaboration model initialization failed")
	}
	strategy, err := diagnostic.NewCaseBasedStrategy(diagnostic.NewAgent(), incident.NewGormRepository(mysql.DB), 1200*time.Millisecond)
	if err != nil {
		return orchestration.CollaborationRun{}, err
	}
	base, err := orchestration.NewDiagnosticRunner(strategy)
	if err != nil {
		return orchestration.CollaborationRun{}, err
	}
	supervisor, err := orchestration.NewDynamicSupervisor(m, name, map[string]orchestration.AgentRunner{
		orchestration.KnowledgeAgentRole:  orchestration.NewDelegatedKnowledgeRunner(knowledgeapp.NewLazyDefaultAnswerer()),
		orchestration.DiagnosticAgentRole: orchestration.NewDelegatedDiagnosticRunner(base, m),
	}, toolruntime.NewGormAuditor(mysql.DB))
	if err != nil {
		return orchestration.CollaborationRun{}, err
	}
	return supervisor.Run(ctx, input)
}
