//go:build server

package filemaintenance

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CancelUploadAction 取消尚未被业务引用的临时上传。
type CancelUploadAction struct{ db *bun.DB }

// NewCancelUploadAction 创建临时上传取消操作。
func NewCancelUploadAction(db *bun.DB) *CancelUploadAction { return &CancelUploadAction{db: db} }

// Execute 将当前用户尚未发送的文件标记为待清理。
func (a *CancelUploadAction) Execute(ctx context.Context, identity *servermodels.Identity, fileID string) error {
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 已关联消息的文件由附件状态接口管理。
		_, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("status = ?", domain.FileStatusDeleting).Set("expires_at = now()").
			Where("id = ? AND workspace_id = ? AND created_by_user_id = ?", fileID, identity.Workspace.ID, identity.User.ID).
			Where("status IN (?, ?)", domain.FileStatusPending, domain.FileStatusUploaded).
			Where("NOT EXISTS (SELECT 1 FROM message_attachments ma WHERE ma.file_id = f.id)").Exec(ctx)
		if err != nil {
			return fmt.Errorf("cancel file upload: %w", err)
		}
		return nil
	})
}
