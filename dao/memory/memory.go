package memory

import (
	"GopherAI/common/mysql"
	"GopherAI/model"
)

// --- ConversationSummary CRUD ---

// UpsertSummary 创建或更新会话摘要（每个 session 只保留最新一条）
func UpsertSummary(summary *model.ConversationSummary) error {
	var existing model.ConversationSummary
	err := mysql.DB.Where("session_id = ?", summary.SessionID).First(&existing).Error
	if err != nil {
		return mysql.DB.Create(summary).Error
	}
	existing.Summary = summary.Summary
	existing.TokenCount = summary.TokenCount
	return mysql.DB.Save(&existing).Error
}

// GetSummaryBySessionID 获取指定会话的摘要
func GetSummaryBySessionID(sessionID string) (*model.ConversationSummary, error) {
	var summary model.ConversationSummary
	err := mysql.DB.Where("session_id = ?", sessionID).First(&summary).Error
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// GetSummariesByUserName 获取某用户所有会话的摘要
func GetSummariesByUserName(userName string) ([]model.ConversationSummary, error) {
	var summaries []model.ConversationSummary
	err := mysql.DB.Where("user_name = ?", userName).Find(&summaries).Error
	return summaries, err
}

// --- MemoryEntry CRUD ---

// CreateMemoryEntry 创建长期记忆条目
func CreateMemoryEntry(entry *model.MemoryEntry) error {
	return mysql.DB.Create(entry).Error
}

// GetMemoryEntriesByUserName 获取某用户的所有长期记忆，按创建时间倒序
func GetMemoryEntriesByUserName(userName string, limit int) ([]model.MemoryEntry, error) {
	var entries []model.MemoryEntry
	query := mysql.DB.Where("user_name = ?", userName).Order("created_at desc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&entries).Error
	return entries, err
}

// GetMemoryEntriesByCategory 按类别获取某用户的长期记忆
func GetMemoryEntriesByCategory(userName, category string) ([]model.MemoryEntry, error) {
	var entries []model.MemoryEntry
	err := mysql.DB.Where("user_name = ? AND category = ?", userName, category).
		Order("created_at desc").Find(&entries).Error
	return entries, err
}

// DeleteMemoryEntry 删除指定记忆条目
func DeleteMemoryEntry(id uint) error {
	return mysql.DB.Delete(&model.MemoryEntry{}, id).Error
}

// DeleteMemoryEntriesByUserName 删除某用户的全部长期记忆
func DeleteMemoryEntriesByUserName(userName string) error {
	return mysql.DB.Where("user_name = ?", userName).Delete(&model.MemoryEntry{}).Error
}

// CountMemoryEntries 统计某用户的记忆条目数
func CountMemoryEntries(userName string) (int64, error) {
	var count int64
	err := mysql.DB.Model(&model.MemoryEntry{}).Where("user_name = ?", userName).Count(&count).Error
	return count, err
}

// PruneOldestEntries 当超出上限时删除最旧的条目，保留 keepCount 条
func PruneOldestEntries(userName string, keepCount int) error {
	var entries []model.MemoryEntry
	err := mysql.DB.Where("user_name = ?", userName).Order("created_at desc").
		Offset(keepCount).Find(&entries).Error
	if err != nil || len(entries) == 0 {
		return err
	}
	ids := make([]uint, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return mysql.DB.Delete(&model.MemoryEntry{}, ids).Error
}
