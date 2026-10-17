//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WorkspaceMediaSetting 保存工作区媒体理解兜底模型。
type WorkspaceMediaSetting struct {
	bun.BaseModel         `bun:"table:workspace_media_settings,alias:wms"`
	WorkspaceID           string    `bun:"workspace_id,pk"`
	OCRModelID            *string   `bun:"ocr_model_id"`
	TranscriptionModelID  *string   `bun:"transcription_model_id"`
	CreatedAt             time.Time `bun:"created_at"`
	UpdatedAt             time.Time `bun:"updated_at"`
}
