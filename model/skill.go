package model

import (
	"time"

	"gorm.io/gorm"
)

// Skill 系统技能定义表，记录平台所有已注册的技能元数据
type Skill struct {
	ID          uint           `gorm:"primaryKey;autoIncrement" json:"id"`
	Code        string         `gorm:"uniqueIndex;type:varchar(64);not null" json:"code"`
	Name        string         `gorm:"type:varchar(128);not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	Type        string         `gorm:"type:varchar(32);not null" json:"type"`        // mcp / local / http
	Status      int            `gorm:"default:1" json:"status"`                      // 1=启用 0=禁用
	ConfigJSON  string         `gorm:"type:text" json:"config_json,omitempty"`       // 扩展配置（JSON字符串）
	Tags        string         `gorm:"type:varchar(256)" json:"tags,omitempty"`      // 逗号分隔标签
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// UserSkill 用户技能开关表，记录某用户是否启用了某技能
type UserSkill struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserName  string    `gorm:"index;type:varchar(64);not null" json:"username"`
	SkillCode string    `gorm:"type:varchar(64);not null" json:"skill_code"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillInvocation 技能调用日志表，记录每次技能执行的完整链路
type SkillInvocation struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TraceID    string    `gorm:"type:varchar(36);index" json:"trace_id"`
	UserName   string    `gorm:"type:varchar(64);index" json:"username"`
	SessionID  string    `gorm:"type:varchar(36);index" json:"session_id"`
	SkillCode  string    `gorm:"type:varchar(64)" json:"skill_code"`
	InputJSON  string    `gorm:"type:text" json:"input_json"`
	OutputJSON string    `gorm:"type:text" json:"output_json"`
	Status     string    `gorm:"type:varchar(16)" json:"status"` // success / failed
	LatencyMs  int64     `json:"latency_ms"`
	Error      string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
