//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// newAIWorkspace 建立带一个对话模型的独立工作区，返回数据库、管理员身份、模型服务编号和模型标识。
func newAIWorkspace(t *testing.T) (*bun.DB, *servermodels.Identity, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	identity := installWorkspace(t, db, workspaceSpec{
		Name: "AI 员工测试", DisplayName: "管理员", Email: uniqueEmail("admin"), Password: "password123",
		Locale: domain.LocaleEnglishUnitedStates, TimeZone: "America/New_York",
	}).Identity
	provider := &servermodels.AIProvider{
		OrganizationID: identity.Organization.ID,
		Brand:          string(domain.AIProviderBrandOpenAI),
		Name:           "测试模型服务",
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey),
		APIKey:         "test-key",
		APIURL:         "https://example.com/v1",
	}
	if _, err := db.NewInsert().Model(provider).
		Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").
		Returning("id").
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	model := &servermodels.AIProviderModel{
		ProviderID: provider.ID, OrganizationID: identity.Organization.ID,
		Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	if _, err := db.NewInsert().Model(model).
		Column("provider_id", "organization_id", "identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return db, identity, provider.ID, model.Identifier
}
