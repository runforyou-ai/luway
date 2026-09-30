//go:build server

package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrLinkedImageNotFound 表示待关联的图片文件不存在、用途不符、已过期或已被其他资料占用。
var ErrLinkedImageNotFound = errors.New("linked image file not found")

// ActivateLinkedImage 在调用方事务中锁定并激活指定用途的已上传图片；当前已关联的图片按原样保留。
func ActivateLinkedImage(ctx context.Context, tx bun.Tx, organizationID string, purpose domain.FilePurpose, fileID string, currentFileID *string) (*string, error) {
	if !common.ValidUUID(fileID) {
		return nil, ErrLinkedImageNotFound
	}
	file := &servermodels.File{}
	err := tx.NewSelect().Model(file).
		Column("id", "status").
		ColumnExpr(expiredColumn).
		Where("f.id = ?", fileID).
		Where("f.organization_id = ?", organizationID).
		Where("f.purpose = ?", purpose).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLinkedImageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock linked image: %w", err)
	}
	sameImage := currentFileID != nil && *currentFileID == file.ID
	if file.Status == string(domain.FileStatusUploaded) {
		if file.Expired {
			return nil, ErrLinkedImageNotFound
		}
		result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("status = ?", domain.FileStatusActive).
			Set("expires_at = NULL").
			Set("updated_at = now()").
			Where("id = ?", file.ID).
			Where("status = ?", domain.FileStatusUploaded).
			Exec(ctx)
		if err != nil {
			return nil, fmt.Errorf("activate linked image: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read activated linked image count: %w", err)
		}
		if rows != 1 {
			return nil, ErrLinkedImageNotFound
		}
	} else if file.Status != string(domain.FileStatusActive) || !sameImage {
		return nil, ErrLinkedImageNotFound
	}
	return &file.ID, nil
}

// RetireLinkedImage 将已经解除资料关联的旧图片交给清理任务删除。
func RetireLinkedImage(ctx context.Context, tx bun.Tx, organizationID string, previousFileID, nextFileID *string) error {
	if previousFileID == nil || nextFileID == nil || *previousFileID == *nextFileID {
		return nil
	}
	if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
		Set("status = ?", domain.FileStatusDeleting).
		Set("expires_at = now()").
		Set("updated_at = now()").
		Where("id = ?", *previousFileID).
		Where("organization_id = ?", organizationID).
		Where("status = ?", domain.FileStatusActive).
		Exec(ctx); err != nil {
		return fmt.Errorf("retire previous linked image: %w", err)
	}
	return nil
}
