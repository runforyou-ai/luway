//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// File 表示企业上传文件的元数据。
type File struct {
	bun.BaseModel `bun:"table:files,alias:f"`

	ID                string  `bun:"id,pk"`
	OrganizationID    string  `bun:"organization_id"`
	CreatedByUserID   string  `bun:"created_by_user_id"`
	Purpose           string  `bun:"purpose"`
	MultipartUploadID *string `bun:"multipart_upload_id"`
	PartSize          int64   `bun:"part_size"`
	ExternalID        *string `bun:"external_id"`
	// UploaderChannelIdentityID 在渠道访客上传的文件上保存其渠道身份，成员上传为空。
	UploaderChannelIdentityID *string    `bun:"uploader_channel_identity_id"`
	StorageBackend            string     `bun:"storage_backend"`
	StorageKey                string     `bun:"storage_key"`
	OriginalName              string     `bun:"original_name"`
	ContentType               string     `bun:"content_type"`
	ByteSize                  int64      `bun:"byte_size"`
	Status                    string     `bun:"status"`
	ETag                      *string    `bun:"etag"`
	UploadedAt                *time.Time `bun:"uploaded_at"`
	ExpiresAt                 *time.Time `bun:"expires_at"`
	CreatedAt                 time.Time  `bun:"created_at"`
	UpdatedAt                 time.Time  `bun:"updated_at"`

	// Expired 是查询时按数据库时钟计算的过期标记，与写入侧的 now() 同源，不对应实际列。
	Expired bool `bun:"expired,scanonly"`
}
