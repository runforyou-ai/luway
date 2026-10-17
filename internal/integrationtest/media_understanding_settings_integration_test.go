//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestMediaUnderstandingSettings 验证工作区可配置 OCR / 转写兜底模型，且须满足对应用途。
func TestMediaUnderstandingSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "媒体理解", DisplayName: "管理员", Email: servertest.UniqueEmail("media-settings"), Password: "password123"})
	deployment := servertest.TestDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	settings, err := backend.GetMediaUnderstandingSettings(ctx, meta)
	require.NoError(t, err)
	require.Nil(t, settings.OCRModelID)
	require.Nil(t, settings.TranscriptionModelID)

	provider := &servermodels.AIProvider{
		WorkspaceID: &owner.Identity.Workspace.ID, Brand: string(domain.AIProviderBrandOpenAICompatible), Name: "本地",
		CredentialType: string(domain.AIProviderCredentialTypeNone), APIKey: "", APIURL: "https://models.test/v1",
	}
	_, err = db.NewInsert().Model(provider).Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx)
	require.NoError(t, err)
	vision := &testAIModel{ProviderID: provider.ID, Identifier: "vision", Name: "识图", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text","image"]`), ContextWindow: 8192, MaxOutputTokens: 1024}
	audio := &testAIModel{ProviderID: provider.ID, Identifier: "audio", Name: "听写", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text","audio"]`), ContextWindow: 8192, MaxOutputTokens: 1024}
	textOnly := &testAIModel{ProviderID: provider.ID, Identifier: "text", Name: "纯文本", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 8192, MaxOutputTokens: 1024}
	insertAIModels(t, db, vision, audio, textOnly)

	_, err = backend.UpdateMediaUnderstandingSettings(ctx, meta, appservice.MediaUnderstandingSettings{OCRModelID: &textOnly.ID})
	require.Error(t, err, "纯文本模型不能用作 OCR")

	saved, err := backend.UpdateMediaUnderstandingSettings(ctx, meta, appservice.MediaUnderstandingSettings{OCRModelID: &vision.ID, TranscriptionModelID: &audio.ID})
	require.NoError(t, err)
	require.Equal(t, vision.ID, *saved.OCRModelID)
	require.Equal(t, audio.ID, *saved.TranscriptionModelID)

	loaded, err := backend.GetMediaUnderstandingSettings(ctx, meta)
	require.NoError(t, err)
	require.Equal(t, vision.ID, *loaded.OCRModelID)
	require.Equal(t, audio.ID, *loaded.TranscriptionModelID)
}
