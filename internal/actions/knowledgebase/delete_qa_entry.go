//go:build server

package knowledgebase

import (
	"context"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteQAEntryAction 删除一条完整问答。
type DeleteQAEntryAction struct{ db *bun.DB }

// NewDeleteQAEntryAction 创建问答删除操作。
func NewDeleteQAEntryAction(db *bun.DB) *DeleteQAEntryAction { return &DeleteQAEntryAction{db: db} }

// Execute 校验归属并在同一事务内删除问答、全部内容和已发布分段。
func (a *DeleteQAEntryAction) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID, entryID string) error {
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		base, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, knowledgeBaseID)
		if err != nil {
			return err
		}
		if err := validateQAKnowledgeBase(base); err != nil {
			return err
		}
		entry, err := lockQAEntry(ctx, tx, knowledgeBaseID, entryID)
		if err != nil {
			return err
		}
		if err := deleteSourceSegments(ctx, tx, entry.ID); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.KnowledgeQAContent)(nil)).Where("entry_id = ?", entry.ID).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model(entry).WherePK().Exec(ctx)
		return err
	})
}
