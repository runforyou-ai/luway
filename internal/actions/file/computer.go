//go:build server

package file

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateComputerUpload 为电脑同步会话共享文件创建待上传的内容文件：用途为会话文件，整体上传不分片，上传用户为 userID，上传电脑为 computerID。
func CreateComputerUpload(ctx context.Context, db bun.IDB, workspaceID, userID, computerID string, input UploadInput) (*servermodels.File, error) {
	input.Purpose = domain.FilePurposeConversationFile
	input, fields := NormalizeUploadInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	record, err := pendingFile(workspaceID, userID, input, 0)
	if err != nil {
		return nil, err
	}
	record.UploaderComputerID = &computerID
	return record, insertPendingFile(ctx, db, record)
}

// CompleteComputerUpload 推进电脑为同步会话共享文件上传的内容文件。
func CompleteComputerUpload(ctx context.Context, db *bun.DB, workspaceID, computerID, fileID string, finalize FinalizeFunc) (*servermodels.File, error) {
	return completeUpload(ctx, db, uploadScope{
		workspaceID: workspaceID,
		condition:   "f.purpose = ? AND f.uploader_computer_id = ?",
		args:        []any{domain.FilePurposeConversationFile, computerID},
	}, fileID, finalize)
}
