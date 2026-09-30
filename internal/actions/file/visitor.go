//go:build server

package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// VisitorUploadInput 定义渠道访客待上传文件的归属与元数据。
type VisitorUploadInput struct {
	OrganizationID    string
	CreatedByUserID   string
	ChannelIdentityID string
	Upload            UploadInput
}

// CreateVisitorPending 在业务事务中创建渠道访客上传的临时文件，访客不使用分片上传。
func CreateVisitorPending(ctx context.Context, db bun.IDB, backend domain.FileStorageBackend, input VisitorUploadInput) (*servermodels.File, error) {
	upload, fields := NormalizeUploadInput(input.Upload)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	record, err := pendingFile(input.OrganizationID, input.CreatedByUserID, backend, upload, 0)
	if err != nil {
		return nil, err
	}
	record.UploaderChannelIdentityID = &input.ChannelIdentityID
	return record, insertPendingFile(ctx, db, record)
}

// VisitorPendingByStorageKey 按存储键读取指定渠道访客待写入内容的本地文件，企业由文件及其上传渠道身份确定。
func (q *GetQuery) VisitorPendingByStorageKey(ctx context.Context, externalID, storageKey string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := selectFile(q.db, record).
		Join("JOIN contact_channel_identities AS cci ON cci.id = f.uploader_channel_identity_id AND cci.organization_id = f.organization_id").
		Where("f.storage_key = ?", storageKey).
		Where("f.purpose = ? AND f.storage_backend = ?", domain.FilePurposeMessageAttachment, domain.FileStorageBackendLocal).
		Where("cci.external_id = ?", externalID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get visitor upload by storage key: %w", err)
	}
	return record, nil
}
