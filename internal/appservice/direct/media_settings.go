//go:build server

package direct

import (
	"context"

	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type mediaSettingsOps struct {
	db *bun.DB
}

func newMediaSettingsOps(db *bun.DB) *mediaSettingsOps {
	return &mediaSettingsOps{db: db}
}

// GetMediaUnderstandingSettings 读取当前企业的媒体理解设置。
func (o *mediaSettingsOps) GetMediaUnderstandingSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.MediaUnderstandingSettings, error) {
	settings, err := workspaceaction.GetMediaSettings(ctx, o.db, identity)
	if err != nil {
		return appservice.MediaUnderstandingSettings{}, appservice.FailedError(meta, i18n.ErrorMediaUnderstandingSettingsLoadFailed, err)
	}
	return appservice.MediaUnderstandingSettings{OCRModelID: settings.OCRModelID, TranscriptionModelID: settings.TranscriptionModelID}, nil
}

// UpdateMediaUnderstandingSettings 修改当前企业的媒体理解设置。
func (o *mediaSettingsOps) UpdateMediaUnderstandingSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.MediaUnderstandingSettings) (appservice.MediaUnderstandingSettings, error) {
	saved, err := workspaceaction.UpdateMediaSettings(ctx, o.db, identity, workspaceaction.MediaSettings{
		OCRModelID: input.OCRModelID, TranscriptionModelID: input.TranscriptionModelID,
	})
	if err != nil {
		return appservice.MediaUnderstandingSettings{}, dispatch.Catalog{
			dispatch.FieldRule(mediaSettingsFieldKeys), dispatch.SessionRule,
		}.Translate(meta, err, i18n.ErrorMediaUnderstandingSettingsUpdateFailed)
	}
	return appservice.MediaUnderstandingSettings{OCRModelID: saved.OCRModelID, TranscriptionModelID: saved.TranscriptionModelID}, nil
}

var mediaSettingsFieldKeys = map[common.FieldCode]i18n.Key{
	workspaceaction.ValidationOCRModelInvalid:           i18n.FieldOcrModelInvalid,
	workspaceaction.ValidationTranscriptionModelInvalid: i18n.FieldTranscriptionModelInvalid,
}
