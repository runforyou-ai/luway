//go:build server

package file

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// ContentWriter 按文件记录中确定的存储类型写入内容。
type ContentWriter interface {
	Save(context.Context, *servermodels.File, []byte) (string, error)
}

// ImportInput 定义服务端导入联系人头像所需事实。
type ImportInput struct {
	WorkspaceID     string
	CreatedByUserID string
	ExternalID      string
	FileName        string
	ContentType     string
	Data            []byte
}

// ImportAction 把服务端取得的联系人头像写为可激活的临时文件。
type ImportAction struct {
	db      *bun.DB
	backend func() domain.FileStorageBackend
	writer  ContentWriter
}

// NewImportAction 创建服务端文件导入操作，backend 返回新文件写入的存储类型。
func NewImportAction(db *bun.DB, backend func() domain.FileStorageBackend, writer ContentWriter) *ImportAction {
	return &ImportAction{db: db, backend: backend, writer: writer}
}

// Execute 先提交临时元数据，再写入内容并标记为已上传。
func (a *ImportAction) Execute(ctx context.Context, input ImportInput) (*servermodels.File, error) {
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	if input.ExternalID == "" {
		return nil, errors.New("imported file external ID is required")
	}
	metadata, fields := normalizeFileInput(UploadInput{
		Purpose: domain.FilePurposeContactAvatar, FileName: input.FileName,
		ContentType: input.ContentType, ByteSize: int64(len(input.Data)),
	}, domain.FilePurposeContactAvatar)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	backend := a.backend()
	fileID := uuid.NewV7()
	record := &servermodels.File{
		ID: fileID.String(), WorkspaceID: input.WorkspaceID, CreatedByUserID: input.CreatedByUserID,
		Purpose: string(domain.FilePurposeContactAvatar), ExternalID: &input.ExternalID, StorageBackend: string(backend),
		StorageKey:   storageKey(input.WorkspaceID, fileID.String(), metadata.FileName, metadata.ContentType),
		OriginalName: metadata.FileName, ContentType: metadata.ContentType, ByteSize: metadata.ByteSize,
		Status: string(domain.FileStatusPending),
	}
	// 独立提交带过期时间的 pending 元数据后写入文件内容。
	if _, err := a.db.NewInsert().Model(record).
		Value("expires_at", "now() + make_interval(secs => ?)", temporaryFileLifetime.Seconds()).
		Returning("expires_at").Exec(ctx); err != nil {
		return nil, fmt.Errorf("create imported file: %w", err)
	}
	etag, err := a.writer.Save(ctx, record, input.Data)
	if err != nil {
		return nil, fmt.Errorf("write imported file: %w", err)
	}
	result, err := a.db.NewUpdate().Model(record).
		Set("status = ?", domain.FileStatusUploaded).
		Set("etag = ?", support.NilIfZero(etag)).
		Set("uploaded_at = now()").
		Set("expires_at = now() + make_interval(secs => ?)", temporaryFileLifetime.Seconds()).
		Where("f.id = ?", record.ID).
		Where("f.workspace_id = ?", record.WorkspaceID).
		Where("f.status = ?", domain.FileStatusPending).
		Where("f.expires_at > now()").
		Returning("*").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark imported file uploaded: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read imported file update count: %w", err)
	}
	if rows == 0 {
		return nil, ErrFileNotFound
	}
	return record, nil
}

// CreateExternalAttachment 在调用方事务内写入外部渠道媒体取回前的 pending 消息附件文件记录，内容由取回任务写入后激活。
func CreateExternalAttachment(ctx context.Context, db bun.IDB, workspaceID, createdByUserID, externalID string, backend domain.FileStorageBackend, input UploadInput) (*servermodels.File, error) {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return nil, errors.New("external attachment external ID is required")
	}
	if backend != domain.FileStorageBackendLocal && backend != domain.FileStorageBackendS3 {
		return nil, fmt.Errorf("invalid external attachment storage backend %q", backend)
	}
	metadata, fields := normalizeFileInput(input, domain.FilePurposeMessageAttachment)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	fileID := uuid.NewV7()
	record := &servermodels.File{
		ID: fileID.String(), WorkspaceID: workspaceID, CreatedByUserID: createdByUserID,
		Purpose: string(domain.FilePurposeMessageAttachment), ExternalID: &externalID, StorageBackend: string(backend),
		StorageKey:   storageKey(workspaceID, fileID.String(), metadata.FileName, metadata.ContentType),
		OriginalName: metadata.FileName, ContentType: metadata.ContentType, ByteSize: metadata.ByteSize,
		Status: string(domain.FileStatusPending),
	}
	if _, err := db.NewInsert().Model(record).
		Value("expires_at", "now() + make_interval(secs => ?)", temporaryFileLifetime.Seconds()).
		Returning("expires_at, created_at, updated_at").Exec(ctx); err != nil {
		return nil, fmt.Errorf("create external attachment file: %w", err)
	}
	return record, nil
}
