//go:build server

package file

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// VisitorUploadInput 定义渠道访客待上传文件的归属与元数据。
type VisitorUploadInput struct {
	WorkspaceID       string
	CreatedByUserID   string
	ChannelIdentityID string
	Upload            UploadInput
}

// CreateVisitorPending 在业务事务中创建渠道访客上传的临时文件，访客不使用分片上传。
func CreateVisitorPending(ctx context.Context, db bun.IDB, input VisitorUploadInput) (*servermodels.File, error) {
	upload, fields := NormalizeUploadInput(input.Upload)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	record, err := pendingFile(input.WorkspaceID, input.CreatedByUserID, upload, 0)
	if err != nil {
		return nil, err
	}
	record.UploaderChannelIdentityID = &input.ChannelIdentityID
	return record, insertPendingFile(ctx, db, record)
}
