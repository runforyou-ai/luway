//go:build server

package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	// ValidationOCRModelInvalid 表示 OCR 模型不可用或不满足 OCR 用途。
	ValidationOCRModelInvalid common.FieldCode = "OCR_MODEL_INVALID"
	// ValidationTranscriptionModelInvalid 表示转写模型不可用或不满足转写用途。
	ValidationTranscriptionModelInvalid common.FieldCode = "TRANSCRIPTION_MODEL_INVALID"
)

// MediaSettings 是工作区媒体理解设置。
type MediaSettings struct {
	OCRModelID           *string
	TranscriptionModelID *string
}

// LoadMediaSettings 读取工作区媒体理解设置；没有行时返回空设置。
func LoadMediaSettings(ctx context.Context, db bun.IDB, workspaceID string) (MediaSettings, error) {
	setting := &servermodels.WorkspaceMediaSetting{}
	err := db.NewSelect().Model(setting).
		Column("ocr_model_id", "transcription_model_id").
		Where("wms.workspace_id = ?", workspaceID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaSettings{}, nil
	}
	if err != nil {
		return MediaSettings{}, fmt.Errorf("load workspace media settings: %w", err)
	}
	return MediaSettings{OCRModelID: setting.OCRModelID, TranscriptionModelID: setting.TranscriptionModelID}, nil
}

// GetMediaSettings 返回当前企业的 OCR 与转写模型编号。
func GetMediaSettings(ctx context.Context, db bun.IDB, identity *servermodels.Identity) (MediaSettings, error) {
	return LoadMediaSettings(ctx, db, identity.Workspace.ID)
}

// UpdateMediaSettings 校验并保存 OCR / 转写模型；nil 表示关闭对应投影。
func UpdateMediaSettings(ctx context.Context, db *bun.DB, identity *servermodels.Identity, input MediaSettings) (MediaSettings, error) {
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if input.OCRModelID != nil {
			if _, err := aimodel.Lock(ctx, tx, identity.Workspace.ID, *input.OCRModelID, domain.AIModelUsageOCR); errors.Is(err, aimodel.ErrUnavailable) {
				return &common.FieldError{Fields: map[string]common.FieldCode{"ocrModelId": ValidationOCRModelInvalid}}
			} else if err != nil {
				return fmt.Errorf("check OCR model: %w", err)
			}
		}
		if input.TranscriptionModelID != nil {
			if _, err := aimodel.Lock(ctx, tx, identity.Workspace.ID, *input.TranscriptionModelID, domain.AIModelUsageTranscription); errors.Is(err, aimodel.ErrUnavailable) {
				return &common.FieldError{Fields: map[string]common.FieldCode{"transcriptionModelId": ValidationTranscriptionModelInvalid}}
			} else if err != nil {
				return fmt.Errorf("check transcription model: %w", err)
			}
		}
		setting := &servermodels.WorkspaceMediaSetting{
			WorkspaceID: identity.Workspace.ID, OCRModelID: input.OCRModelID, TranscriptionModelID: input.TranscriptionModelID,
		}
		_, err := tx.NewInsert().Model(setting).
			Column("workspace_id", "ocr_model_id", "transcription_model_id").
			On("CONFLICT (workspace_id) DO UPDATE").
			Set("ocr_model_id = EXCLUDED.ocr_model_id").
			Set("transcription_model_id = EXCLUDED.transcription_model_id").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save workspace media settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return MediaSettings{}, err
	}
	return input, nil
}
