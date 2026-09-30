//go:build server

package file

import (
	"context"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// SetMultipartUpload 保存已创建的对象存储分片会话。
func (a *CreateUploadAction) SetMultipartUpload(ctx context.Context, identity *servermodels.Identity, fileID, uploadID string) (bool, error) {
	stored := false
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("multipart_upload_id = ?", uploadID).
			Where("id = ? AND organization_id = ? AND created_by_user_id = ?", fileID, identity.Organization.ID, identity.User.ID).
			Where("status = ? AND expires_at > now() AND multipart_upload_id IS NULL", domain.FileStatusPending).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		stored = count == 1
		return nil
	})
	return stored, err
}

// UploadPartSize 返回有效分片的预期字节数。
func UploadPartSize(record *servermodels.File, number int32) (int64, error) {
	if record.PartSize <= 0 || number <= 0 || int64(number) > (record.ByteSize-1)/record.PartSize+1 {
		return 0, ErrFileNotFound
	}
	return min(record.PartSize, record.ByteSize-int64(number-1)*record.PartSize), nil
}
