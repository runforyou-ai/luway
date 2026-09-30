//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// KnowledgeDocumentContent 保存在线文档正文或网页抓取快照。
type KnowledgeDocumentContent struct {
	bun.BaseModel `bun:"table:knowledge_document_contents,alias:kdc"`
	DocumentID    string    `bun:"document_id,pk"`
	Content       string    `bun:"content"`
	CreatedAt     time.Time `bun:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at"`
}
