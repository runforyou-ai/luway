//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// KnowledgeQAContent 表示具有稳定编号的主问题、相似问题或答案。
type KnowledgeQAContent struct {
	bun.BaseModel `bun:"table:knowledge_qa_contents,alias:kqc"`
	ID            string                        `bun:"id,pk"`
	EntryID       string                        `bun:"entry_id"`
	Kind          domain.KnowledgeQAContentKind `bun:"kind"`
	Content       string                        `bun:"content"`
	SortOrder     int                           `bun:"sort_order"`
	CreatedAt     time.Time                     `bun:"created_at"`
	UpdatedAt     time.Time                     `bun:"updated_at"`
}
