//go:build server

package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// FinalizeFunc 完成文件内容写入并返回 ETag 和实际字节数。
type FinalizeFunc func(ctx context.Context, record *servermodels.File) (etag string, byteSize int64, err error)

// uploadScope 定义完成上传时文件须满足的归属条件；identity 非空时按成员上传在事务中锁定活跃用户。
type uploadScope struct {
	organizationID string
	condition      string
	args           []any
	identity       *servermodels.Identity
}

// CompleteUploadAction 核验文件内容并将上传标记为完成。
type CompleteUploadAction struct {
	db *bun.DB
}

// NewCompleteUploadAction 创建文件上传完成操作。
func NewCompleteUploadAction(db *bun.DB) *CompleteUploadAction {
	return &CompleteUploadAction{db: db}
}

// Execute 推进当前成员创建的文件上传。
func (a *CompleteUploadAction) Execute(ctx context.Context, identity *servermodels.Identity, fileID string, finalize FinalizeFunc) (*servermodels.File, error) {
	return completeUpload(ctx, a.db, uploadScope{
		organizationID: identity.Organization.ID,
		condition:      "f.created_by_user_id = ?",
		args:           []any{identity.User.ID},
		identity:       identity,
	}, fileID, finalize)
}

// CompleteVisitorUpload 推进渠道访客上传的消息附件。
func CompleteVisitorUpload(ctx context.Context, db *bun.DB, organizationID, channelIdentityID, fileID string, finalize FinalizeFunc) (*servermodels.File, error) {
	return completeUpload(ctx, db, uploadScope{
		organizationID: organizationID,
		condition:      "f.purpose = ? AND f.uploader_channel_identity_id = ?",
		args:           []any{domain.FilePurposeMessageAttachment, channelIdentityID},
	}, fileID, finalize)
}

// completeUpload 按文件当前状态推进上传：已激活或已上传且未过期时幂等返回，待上传时核验内容后标记完成。
func completeUpload(ctx context.Context, db *bun.DB, scope uploadScope, fileID string, finalize FinalizeFunc) (*servermodels.File, error) {
	record, err := loadScopedUpload(ctx, db, scope, fileID)
	if err != nil {
		return nil, err
	}
	if record.Status != string(domain.FileStatusPending) {
		return settledUpload(record)
	}
	if record.Expired {
		return nil, ErrFileNotFound
	}
	etag, actualSize, err := finalize(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("finalize uploaded file: %w", err)
	}
	if actualSize != record.ByteSize {
		return nil, fmt.Errorf("uploaded file size = %d, want %d", actualSize, record.ByteSize)
	}
	marked, err := markUploaded(ctx, db, scope, record.ID, etag)
	if !errors.Is(err, ErrFileNotFound) {
		return marked, err
	}
	// 守卫更新未命中时重读一次，并发核验已完成状态转移则按幂等结果返回。
	record, err = loadScopedUpload(ctx, db, scope, fileID)
	if err != nil {
		return nil, err
	}
	return settledUpload(record)
}

// loadScopedUpload 读取归属范围内的文件。
func loadScopedUpload(ctx context.Context, db bun.IDB, scope uploadScope, fileID string) (*servermodels.File, error) {
	if !common.ValidUUID(fileID) {
		return nil, ErrFileNotFound
	}
	record := &servermodels.File{}
	err := selectFile(db, record).
		Where("f.id = ? AND f.organization_id = ?", fileID, scope.organizationID).
		Where(scope.condition, scope.args...).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get upload: %w", err)
	}
	return record, nil
}

// settledUpload 返回已激活或已上传且未过期的文件，其余状态按文件不可用处理。
func settledUpload(record *servermodels.File) (*servermodels.File, error) {
	if record.Status == string(domain.FileStatusActive) || (record.Status == string(domain.FileStatusUploaded) && !record.Expired) {
		return record, nil
	}
	return nil, ErrFileNotFound
}

// markUploaded 保存核验结果并返回最新记录，状态转移由带 status 和 expires_at 守卫的原子 UPDATE 保证。
func markUploaded(ctx context.Context, db *bun.DB, scope uploadScope, fileID, etag string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if scope.identity != nil {
			if err := identityaction.LockActiveUser(ctx, tx, scope.identity); err != nil {
				return err
			}
		}
		// 过期时间统一使用数据库时钟，与同一语句里 expires_at > now() 的比较保持同源。
		result, err := tx.NewUpdate().Model(record).
			Set("status = ?", domain.FileStatusUploaded).
			Set("etag = ?", common.OptionalString(strings.TrimSpace(etag))).
			Set("uploaded_at = now()").
			Set("expires_at = now() + make_interval(secs => ?)", temporaryFileLifetime.Seconds()).
			Set("updated_at = now()").
			Where("f.id = ? AND f.organization_id = ?", fileID, scope.organizationID).
			Where(scope.condition, scope.args...).
			Where("f.status = ?", domain.FileStatusPending).
			Where("f.expires_at > now()").
			Returning("*").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("mark file uploaded: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read marked file count: %w", err)
		}
		if rows == 0 {
			return ErrFileNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}
