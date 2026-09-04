package model

import "time"

type KnowledgeDocument struct {
	ID             string    `gorm:"primaryKey;type:char(36)" json:"id"`
	TenantID       string    `gorm:"index;uniqueIndex:uk_knowledge_documents_tenant_hash,priority:1;not null;type:varchar(64)" json:"tenant_id"`
	UserID         string    `gorm:"index;not null;type:varchar(64)" json:"user_id"`
	DisplayName    string    `gorm:"not null;type:varchar(255)" json:"display_name"`
	MimeType       string    `gorm:"not null;type:varchar(128)" json:"mime_type"`
	CurrentVersion int       `gorm:"not null" json:"current_version"`
	Status         string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	SizeBytes      int64     `gorm:"not null" json:"size_bytes"`
	ContentHash    string    `gorm:"index;uniqueIndex:uk_knowledge_documents_tenant_hash,priority:2;not null;type:char(64)" json:"content_hash"`
	StoragePath    string    `gorm:"not null;type:varchar(512)" json:"-"`
	LastErrorCode  string    `gorm:"type:varchar(64)" json:"last_error_code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type KnowledgeDocumentVersion struct {
	ID               string     `gorm:"primaryKey;type:char(36)" json:"id"`
	DocumentID       string     `gorm:"uniqueIndex:uk_knowledge_document_version,priority:1;not null;type:char(36)" json:"document_id"`
	Version          int        `gorm:"uniqueIndex:uk_knowledge_document_version,priority:2;not null" json:"version"`
	Status           string     `gorm:"index;not null;type:varchar(32)" json:"status"`
	ContentHash      string     `gorm:"not null;type:char(64)" json:"content_hash"`
	StoragePath      string     `gorm:"not null;type:varchar(512)" json:"-"`
	ParserVersion    string     `gorm:"type:varchar(64)" json:"parser_version"`
	ChunkerVersion   string     `gorm:"type:varchar(64)" json:"chunker_version"`
	EmbeddingVersion string     `gorm:"type:varchar(128)" json:"embedding_version"`
	IndexedAt        *time.Time `json:"indexed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type KnowledgeChunk struct {
	ID               string    `gorm:"primaryKey;type:char(36)" json:"id"`
	DocumentID       string    `gorm:"index;uniqueIndex:uk_knowledge_chunks_identity,priority:1;not null;type:char(36)" json:"document_id"`
	DocumentVersion  int       `gorm:"uniqueIndex:uk_knowledge_chunks_identity,priority:2;not null" json:"document_version"`
	TenantID         string    `gorm:"index;not null;type:varchar(64)" json:"tenant_id"`
	UserID           string    `gorm:"index;not null;type:varchar(64)" json:"user_id"`
	Ordinal          int       `gorm:"uniqueIndex:uk_knowledge_chunks_identity,priority:3;not null" json:"ordinal"`
	SectionPath      string    `gorm:"type:varchar(512)" json:"section_path,omitempty"`
	LineStart        int       `gorm:"not null" json:"line_start"`
	LineEnd          int       `gorm:"not null" json:"line_end"`
	Content          string    `gorm:"type:longtext;not null" json:"content"`
	TokenCount       int       `gorm:"not null" json:"token_count"`
	ContentHash      string    `gorm:"uniqueIndex:uk_knowledge_chunks_identity,priority:4;not null;type:char(64)" json:"content_hash"`
	MetadataJSON     string    `gorm:"type:longtext" json:"metadata_json,omitempty"`
	EmbeddingVersion string    `gorm:"type:varchar(128)" json:"embedding_version,omitempty"`
	IndexStatus      string    `gorm:"index;not null;type:varchar(32)" json:"index_status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type KnowledgeJob struct {
	ID            string    `gorm:"primaryKey;type:char(36)" json:"id"`
	TenantID      string    `gorm:"index;not null;type:varchar(64)" json:"tenant_id"`
	DocumentID    string    `gorm:"index;not null;type:char(36)" json:"document_id"`
	Version       int       `gorm:"not null" json:"version"`
	JobType       string    `gorm:"index;not null;type:varchar(64)" json:"job_type"`
	Status        string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	Attempt       int       `gorm:"not null" json:"attempt"`
	LastErrorCode string    `gorm:"type:varchar(64)" json:"last_error_code,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type OutboxEvent struct {
	ID               string     `gorm:"primaryKey;type:char(36)" json:"id"`
	Topic            string     `gorm:"index;not null;type:varchar(128)" json:"topic"`
	EventType        string     `gorm:"index;not null;type:varchar(128)" json:"event_type"`
	TraceID          string     `gorm:"index;not null;type:varchar(36)" json:"trace_id"`
	TenantID         string     `gorm:"index;not null;type:varchar(64)" json:"tenant_id"`
	AggregateID      string     `gorm:"index;not null;type:char(36)" json:"aggregate_id"`
	AggregateVersion int        `gorm:"not null" json:"aggregate_version"`
	PayloadJSON      string     `gorm:"type:longtext;not null" json:"payload_json"`
	Status           string     `gorm:"index;not null;type:varchar(32)" json:"status"`
	Attempt          int        `gorm:"not null" json:"attempt"`
	AvailableAt      time.Time  `gorm:"index;not null" json:"available_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	LastErrorCode    string     `gorm:"type:varchar(64)" json:"last_error_code,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
