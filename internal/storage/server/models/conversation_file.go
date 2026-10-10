//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ConversationFile 表示会话共享文件区中的一个文件及其当前版本。
type ConversationFile struct {
	bun.BaseModel `bun:"table:conversation_files,alias:cf"`

	ID                 string    `bun:"id,pk"`
	CreatedAt          time.Time `bun:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at"`
	WorkspaceID        string    `bun:"workspace_id"`
	ConversationID     string    `bun:"conversation_id"`
	Path               string    `bun:"path"`
	FileID             string    `bun:"file_id"`
	ContentHash        string    `bun:"content_hash"`
	UpdatedBySubjectID string    `bun:"updated_by_subject_id"`
}
