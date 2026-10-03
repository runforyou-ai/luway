//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/runforyou-ai/luway/internal/actions/modelcall"
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
		OrganizationID: &identity.Organization.ID,
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
	model := &testAIModel{
		ProviderID: provider.ID, Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	insertAIModels(t, db, model)
	return db, identity, provider.ID, model.ID
}

// testAIModel 定义测试写入的工作区模型及其指向供应商的来源。
type testAIModel struct {
	ID              string          `bun:"id"`
	ProviderID      string          `bun:"provider_id"`
	Identifier      string          `bun:"identifier"`
	Name            string          `bun:"name"`
	Type            string          `bun:"type"`
	InputModalities json.RawMessage `bun:"input_modalities"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
}

// insertAIModels 按供应商所属工作区写入模型与指向该供应商的来源路由，并回填模型编号。
func insertAIModels(t *testing.T, db bun.IDB, models ...*testAIModel) {
	t.Helper()
	ctx := context.Background()
	for _, model := range models {
		var organizationID string
		if err := db.NewSelect().Model((*servermodels.AIProvider)(nil)).Column("organization_id").
			Where("id = ?", model.ProviderID).Scan(ctx, &organizationID); err != nil {
			t.Fatal(err)
		}
		record := &servermodels.AIModel{
			OrganizationID: &organizationID, Name: model.Name, Type: model.Type, InputModalities: model.InputModalities,
			ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
		}
		if _, err := db.NewInsert().Model(record).
			Column("organization_id", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Returning("id").
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
		route := &servermodels.AIModelRoute{ModelID: record.ID, ProviderID: model.ProviderID, Identifier: model.Identifier, Enabled: true}
		if _, err := db.NewInsert().Model(route).Column("model_id", "provider_id", "identifier", "priority", "enabled").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		model.ID = record.ID
	}
}

// aiModelID 返回供应商下指定上游模型标识的模型编号。
func aiModelID(t *testing.T, db bun.IDB, providerID, identifier string) string {
	t.Helper()
	var id string
	if err := db.NewSelect().Model((*servermodels.AIModelRoute)(nil)).
		ColumnExpr("model_id::text").
		Where("provider_id = ? AND identifier = ?", providerID, identifier).
		Scan(context.Background(), &id); err != nil {
		t.Fatalf("查询模型 %s/%s 编号失败：%v", providerID, identifier, err)
	}
	return id
}

// loadTestAIModel 读取工作区模型及其来源的供应商与上游模型标识。
func loadTestAIModel(t *testing.T, db bun.IDB, modelID string) *testAIModel {
	t.Helper()
	model := &testAIModel{}
	if err := db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, amr.provider_id::text AS provider_id, amr.identifier, aim.name, aim.model_type AS type").
		ColumnExpr("aim.input_modalities, aim.context_window, aim.max_output_tokens").
		Join("JOIN ai_model_routes AS amr ON amr.model_id = aim.id").
		Where("aim.id = ?", modelID).
		Scan(context.Background(), model); err != nil {
		t.Fatalf("读取模型 %s 失败：%v", modelID, err)
	}
	return model
}

// testModelInvoker 返回使用默认上游客户端的统一调用入口，供不请求真实上游的测试装配业务操作。
func testModelInvoker(db bun.IDB) *modelcall.Invoker {
	return modelcall.New(db, modelcall.DefaultUpstreams())
}
