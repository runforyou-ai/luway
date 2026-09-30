//go:build server

package file

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"uuid"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateUploadAction 创建待上传文件记录。
type CreateUploadAction struct {
	db *bun.DB
}

// NewCreateUploadAction 创建文件上传操作。
func NewCreateUploadAction(db *bun.DB) *CreateUploadAction {
	return &CreateUploadAction{db: db}
}

// Execute 校验元数据并创建指定存储位置的待上传文件。
func (a *CreateUploadAction) Execute(ctx context.Context, identity *servermodels.Identity, backend domain.FileStorageBackend, input UploadInput) (*servermodels.File, error) {
	input, fields := NormalizeUploadInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var record *servermodels.File
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var err error
		record, err = CreatePending(ctx, tx, identity, backend, input)
		return err
	})
	return record, err
}

// CreatePending 使用已规范化的元数据在业务事务中创建文件记录。
func CreatePending(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, backend domain.FileStorageBackend, input UploadInput) (*servermodels.File, error) {
	partSize := int64(0)
	if input.ByteSize > domain.FilePartSize {
		// 大文件按 S3 最多 10000 片增大片大小。
		partSize = max(domain.FilePartSize, (input.ByteSize-1)/10000+1)
	}
	record, err := pendingFile(identity.Organization.ID, identity.User.ID, backend, input, partSize)
	if err != nil {
		return nil, err
	}
	return record, insertPendingFile(ctx, tx, record)
}

// pendingFile 按归属、存储位置和分片大小构造待上传文件记录。
func pendingFile(organizationID, createdByUserID string, backend domain.FileStorageBackend, input UploadInput, partSize int64) (*servermodels.File, error) {
	if backend != domain.FileStorageBackendLocal && backend != domain.FileStorageBackendS3 {
		return nil, fmt.Errorf("invalid file storage backend %q", backend)
	}
	id := uuid.NewV7().String()
	record := &servermodels.File{ID: id, OrganizationID: organizationID, CreatedByUserID: createdByUserID,
		Purpose: string(input.Purpose), StorageBackend: string(backend), StorageKey: storageKey(organizationID, id, input.FileName, input.ContentType),
		OriginalName: input.FileName, ContentType: input.ContentType, ByteSize: input.ByteSize, PartSize: partSize, Status: string(domain.FileStatusPending)}
	// 知识文档原件使用独立目录，读取时必须校验登录身份与文件状态。
	if input.Purpose == domain.FilePurposeKnowledgeDocument {
		record.StorageKey = "organizations/" + organizationID + "/knowledge-documents/" + record.ID + ".bin"
	}
	return record, nil
}

// insertPendingFile 写入待上传文件并设置临时过期时间。
func insertPendingFile(ctx context.Context, db bun.IDB, record *servermodels.File) error {
	if _, err := db.NewInsert().Model(record).Value("expires_at", "now() + make_interval(secs => ?)", temporaryFileLifetime.Seconds()).Returning("expires_at").Exec(ctx); err != nil {
		return fmt.Errorf("create file upload: %w", err)
	}
	return nil
}

// storageExtensionPattern 限定存储键扩展名为单段小写字母或数字，公开路径按单段扩展名解析文件编号。
var storageExtensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,16}$`)

// storageFileExtensions 列出原始文件名缺少可用扩展名时按内容类型补全的常用扩展名。
var storageFileExtensions = map[string]string{
	"image/jpeg":         ".jpg",
	"image/png":          ".png",
	"image/webp":         ".webp",
	"image/gif":          ".gif",
	"image/svg+xml":      ".svg",
	"image/bmp":          ".bmp",
	"image/heic":         ".heic",
	"application/pdf":    ".pdf",
	"application/msword": ".doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
	"application/vnd.ms-excel": ".xls",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.ms-powerpoint":                                             ".ppt",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
	"application/vnd.oasis.opendocument.text":                                   ".odt",
	"application/vnd.oasis.opendocument.spreadsheet":                            ".ods",
	"application/vnd.oasis.opendocument.presentation":                           ".odp",
	"application/rtf":             ".rtf",
	"text/plain":                  ".txt",
	"text/markdown":               ".md",
	"text/csv":                    ".csv",
	"text/html":                   ".html",
	"application/json":            ".json",
	"application/xml":             ".xml",
	"application/zip":             ".zip",
	"application/x-7z-compressed": ".7z",
	"application/vnd.rar":         ".rar",
	"application/gzip":            ".gz",
	"application/x-tar":           ".tar",
	"audio/mpeg":                  ".mp3",
	"audio/wav":                   ".wav",
	"audio/mp4":                   ".m4a",
	"video/mp4":                   ".mp4",
	"video/quicktime":             ".mov",
	"video/webm":                  ".webm",
}

// storageKey 返回以文件编号命名的存储键，扩展名优先取原始文件名，其次按内容类型补全，都不可用时为 .bin。
func storageKey(organizationID, fileID, fileName, contentType string) string {
	extension := strings.ToLower(path.Ext(fileName))
	if !storageExtensionPattern.MatchString(extension) {
		extension = storageFileExtensions[contentType]
	}
	if extension == "" {
		extension = ".bin"
	}
	return "organizations/" + organizationID + "/files/" + fileID + extension
}
