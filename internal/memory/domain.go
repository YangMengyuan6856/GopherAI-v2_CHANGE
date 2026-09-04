package memory

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SchemaVersion      = "memory-context-v1"
	DefaultWindowLimit = 20
	DefaultWindowTTL   = 24 * time.Hour
	DefaultTokenBudget = 2048
	MaxTokenBudget     = 8192
)

var (
	ErrSessionNotFound = errors.New("memory session not found")
	ErrCacheMiss       = errors.New("working memory cache miss")
	ErrInvalidMessage  = errors.New("invalid working memory message")
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type WorkingMessage struct {
	ID        uint      `json:"id"`
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (message WorkingMessage) Validate() error {
	if message.Role != RoleUser && message.Role != RoleAssistant {
		return fmt.Errorf("%w: unsupported role", ErrInvalidMessage)
	}
	content := strings.TrimSpace(message.Content)
	if content == "" || len([]rune(content)) > 32000 {
		return fmt.Errorf("%w: content must contain 1..32000 characters", ErrInvalidMessage)
	}
	return nil
}

type CacheStatus string

const (
	CacheHit           CacheStatus = "hit"
	CacheRebuilt       CacheStatus = "rebuilt_from_mysql"
	CacheDegradedMySQL CacheStatus = "mysql_fallback_cache_unavailable"
)

type WorkingWindow struct {
	SessionID  string           `json:"session_id"`
	Messages   []WorkingMessage `json:"messages"`
	Cache      CacheStatus      `json:"cache_status"`
	Limit      int              `json:"window_limit"`
	TTLSeconds int64            `json:"cache_ttl_seconds"`
}

type StructuredSummary struct {
	Goal           string            `json:"goal,omitempty"`
	Constraints    []string          `json:"constraints,omitempty"`
	ConfirmedFacts map[string]string `json:"confirmed_facts,omitempty"`
	OpenQuestions  []string          `json:"open_questions,omitempty"`
	CompletedSteps []string          `json:"completed_steps,omitempty"`
	FailedSteps    []string          `json:"failed_steps,omitempty"`
	EvidenceRefs   []string          `json:"evidence_refs,omitempty"`
	NextAction     string            `json:"next_action,omitempty"`
}

type ProfileFact struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

type ContextKind string

const (
	ContextSafetyRule   ContextKind = "safety_rule"
	ContextQuestion     ContextKind = "current_question"
	ContextConstraint   ContextKind = "constraint"
	ContextRunState     ContextKind = "run_state"
	ContextFact         ContextKind = "confirmed_fact"
	ContextOpenQuestion ContextKind = "open_question"
	ContextEvidence     ContextKind = "evidence_ref"
	ContextSummary      ContextKind = "structured_summary"
	ContextProfile      ContextKind = "profile_memory"
	ContextWorking      ContextKind = "working_message"
)

type ContextItem struct {
	Kind            ContextKind `json:"kind"`
	Role            Role        `json:"role,omitempty"`
	Content         string      `json:"content"`
	Required        bool        `json:"required"`
	EstimatedTokens int         `json:"estimated_tokens"`
}

type ContextAssembly struct {
	Version             string        `json:"version"`
	BudgetTokens        int           `json:"budget_tokens"`
	EstimatedTokens     int           `json:"estimated_tokens"`
	OriginalTokens      int           `json:"original_tokens"`
	TokenReductionRatio float64       `json:"token_reduction_ratio"`
	OverBudget          bool          `json:"over_budget"`
	Included            []ContextItem `json:"included"`
	DroppedByBudget     int           `json:"dropped_by_budget"`
	WorkingIncluded     int           `json:"working_messages_included"`
	WorkingAvailable    int           `json:"working_messages_available"`
	ProfileIncluded     int           `json:"profile_memories_included"`
	ProfileAvailable    int           `json:"profile_memories_available"`
}

type ProfileRecallSummary struct {
	PolicyVersion string `json:"policy_version"`
	Status        string `json:"status"`
	Returned      int    `json:"returned"`
}

type Preview struct {
	SchemaVersion string                `json:"schema_version"`
	Window        WorkingWindow         `json:"window"`
	Context       ContextAssembly       `json:"context"`
	ProfileRecall *ProfileRecallSummary `json:"profile_recall,omitempty"`
}
