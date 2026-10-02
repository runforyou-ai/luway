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

// newAIWorkspace 建立带一个对话模型的独立工作区，返回数据库、管理员身份、模型服务编号和模型编号。
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
	model := &servermodels.AIModel{
		ProviderID: provider.ID, Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	insertAIModels(t, db, model)
	return db, identity, provider.ID, model.ID
}

// insertAIModels 写入模型目录项并回填模型编号。
func insertAIModels(t *testing.T, db bun.IDB, models ...*servermodels.AIModel) {
	t.Helper()
	for _, model := range models {
		if _, err := db.NewInsert().Model(model).
			Column("provider_id", "identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Returning("id").
			Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// aiModelID 返回供应商下指定上游模型标识的模型编号。
func aiModelID(t *testing.T, db bun.IDB, providerID, identifier string) string {
	t.Helper()
	var id string
	if err := db.NewSelect().Model((*servermodels.AIModel)(nil)).
		ColumnExpr("id::text").
		Where("provider_id = ? AND identifier = ?", providerID, identifier).
		Scan(context.Background(), &id); err != nil {
		t.Fatalf("查询模型 %s/%s 编号失败：%v", providerID, identifier, err)
	}
	return id
}
