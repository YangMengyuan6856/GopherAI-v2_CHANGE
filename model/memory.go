package model

import "time"

// ConversationSummary 会话摘要，存储对早期对话的 LLM 压缩摘要
type ConversationSummary struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID  string    `gorm:"index;not null;type:varchar(36)" json:"session_id"`
	UserName   string    `gorm:"type:varchar(20)" json:"username"`
	Summary    string    `gorm:"type:text" json:"summary"`
	TokenCount int       `gorm:"default:0" json:"token_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MemoryEntry 长期记忆条目，跨会话存储用户偏好/事实/指令
type MemoryEntry struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserName  string    `gorm:"index;not null;type:varchar(20)" json:"username"`
	Category  string    `gorm:"type:varchar(50)" json:"category"` // preference / fact / instruction
	Content   string    `gorm:"type:text" json:"content"`
	Source    string    `gorm:"type:varchar(36)" json:"source"` // 来源 session_id
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
