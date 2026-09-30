//go:build server

package knowledgebase

import (
	"context"
	"database/sql"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetQAEntryQuery 读取完整问答内容。
type GetQAEntryQuery struct{ db *bun.DB }

// NewGetQAEntryQuery 创建问答详情查询。
func NewGetQAEntryQuery(db *bun.DB) *GetQAEntryQuery { return &GetQAEntryQuery{db: db} }

// Execute 在同一快照中读取当前企业问答及其内容。
func (q *GetQAEntryQuery) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID, entryID string) (*QARecord, error) {
	var record *QARecord
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		base, err := loadKnowledgeBase(ctx, tx, identity.Organization.ID, knowledgeBaseID)
		if err != nil {
			return err
		}
		if err := validateQAKnowledgeBase(base); err != nil {
			return err
		}
		entry, err := loadQAEntry(ctx, tx, knowledgeBaseID, entryID)
		if err != nil {
			return err
		}
		record, err = loadQARecord(ctx, tx, entry)
		return err
	})
	return record, err
}
