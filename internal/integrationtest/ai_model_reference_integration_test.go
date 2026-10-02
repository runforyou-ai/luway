//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestAIModelReferences 验证业务配置按模型编号引用模型：上游标识可改、引用保护、按用途列出与工作区边界。
func TestAIModelReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, providerID, chatModelID := newAIWorkspace(t)
	update := aiprovideraction.NewUpdateAIProviderAction(db, knowledgeaction.NewEmbeddingReindexer(db, newKnowledgeTasks(t, db)))
	get := aiprovideraction.NewGetAIProviderQuery(db)
	// saveModels 以当前供应商配置保存新的模型目录。
	saveModels := func(models []aiprovideraction.Model) (*aiprovideraction.Record, error) {
		provider, err := get.Execute(ctx, identity, providerID)
		if err != nil {
			t.Fatal(err)
		}
		return update.Execute(ctx, identity, providerID, aiprovideraction.UpdateInput{
			Name: provider.Name, CredentialType: provider.CredentialType, APIKey: provider.APIKey, APIURL: provider.APIURL, Models: models,
		})
	}
	provider, err := get.Execute(ctx, identity, providerID)
	if err != nil {
		t.Fatal(err)
	}
	// 新增判断模型时不传编号，由服务端分配。
	saved, err := saveModels(append(provider.Models, aiprovideraction.Model{
		Identifier: "decision-model", Name: "判断模型", Type: domain.AIModelTypeDecision,
		InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Models) != 2 || saved.Models[0].ID != chatModelID || !common.ValidUUID(saved.Models[1].ID) {
		t.Fatalf("saved models=%+v", saved.Models)
	}
	decisionModelID := saved.Models[1].ID

	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "模型引用测试",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: chatModelID, SystemInstruction: "回答问题",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customerserviceaction.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: &decisionModelID, SummaryModelID: &chatModelID, Locale: domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}

	// 修改上游模型标识保留模型编号，业务引用随之使用新标识。
	renamed := slices.Clone(saved.Models)
	renamed[0].Identifier = "chat-model-v2"
	if _, err := saveModels(renamed); err != nil {
		t.Fatal(err)
	}
	loaded, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Execution.Managed.Model.ID != chatModelID {
		t.Fatalf("agent model=%+v", loaded.Execution.Managed.Model)
	}
	resolved, err := aimodel.Resolve(ctx, db, identity.Organization.ID, chatModelID, domain.AIModelUsageSummary)
	if err != nil || len(resolved.Routes) != 1 || resolved.Routes[0].Identifier != "chat-model-v2" || resolved.Routes[0].APIKey != "test-key" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}

	// 被引用的模型不能移除，也不能改成不满足引用用途的类型或输入能力。
	var validation *common.FieldError
	for name, models := range map[string][]aiprovideraction.Model{
		"移除判断模型": {renamed[0]},
		"对话模型改为向量模型": {modelWith(renamed[0], func(model *aiprovideraction.Model) {
			model.Type, model.MaxOutputTokens = domain.AIModelTypeEmbedding, 0
		}), renamed[1]},
		"对话模型去掉文本输入": {modelWith(renamed[0], func(model *aiprovideraction.Model) {
			model.InputModalities = []domain.AIModelInputModality{domain.AIModelInputModalityImage}
		}), renamed[1]},
	} {
		if _, err := saveModels(models); !errors.As(err, &validation) || validation.Fields["models"] != aiprovideraction.ValidationModelsInUse {
			t.Fatalf("%s error=%v", name, err)
		}
	}
	// 引用其他供应商的模型编号或新增与已有模型重复的上游标识均视为目录无效。
	for name, models := range map[string][]aiprovideraction.Model{
		"未知模型编号": {renamed[0], renamed[1], modelWith(renamed[1], func(model *aiprovideraction.Model) {
			model.ID, model.Identifier = uuid.NewV7().String(), "other"
		})},
		"上游标识冲突": {renamed[0], renamed[1], modelWith(renamed[1], func(model *aiprovideraction.Model) {
			model.ID, model.Identifier = "", "chat-model-v2"
		})},
	} {
		if _, err := saveModels(models); !errors.As(err, &validation) || validation.Fields["models"] != aiprovideraction.ValidationModelsInvalid {
			t.Fatalf("%s error=%v", name, err)
		}
	}
	// 一次保存中交换两个模型的上游标识，最终目录不重复即可保存。
	swapped := []aiprovideraction.Model{
		modelWith(renamed[0], func(model *aiprovideraction.Model) { model.Identifier = renamed[1].Identifier }),
		modelWith(renamed[1], func(model *aiprovideraction.Model) { model.Identifier = renamed[0].Identifier }),
	}
	if result, err := saveModels(swapped); err != nil || result.Models[0].ID != chatModelID || result.Models[0].Identifier != "decision-model" {
		t.Fatalf("swap identifiers result=%+v err=%v", result, err)
	}
	if _, err := saveModels(renamed); err != nil {
		t.Fatal(err)
	}
	if err := aiprovideraction.NewDeleteAIProviderAction(db).Execute(ctx, identity, providerID); !errors.Is(err, aiprovideraction.ErrInUse) {
		t.Fatalf("delete provider error=%v", err)
	}

	// 模型选项按用途过滤。
	list := aimodel.NewListOptionsQuery(db)
	for usage, want := range map[domain.AIModelUsage]string{
		domain.AIModelUsageAgent: chatModelID, domain.AIModelUsageTranslation: chatModelID, domain.AIModelUsageDecision: decisionModelID,
	} {
		options, err := list.Execute(ctx, identity, usage)
		if err != nil || len(options) != 1 || options[0].ID != want || options[0].ProviderID != providerID {
			t.Fatalf("usage=%s options=%+v err=%v", usage, options, err)
		}
	}
	if options, err := list.Execute(ctx, identity, domain.AIModelUsageEmbedding); err != nil || len(options) != 0 {
		t.Fatalf("embedding options=%+v err=%v", options, err)
	}
	if _, err := list.Execute(ctx, identity, "unknown"); !errors.Is(err, aimodel.ErrUsageInvalid) {
		t.Fatalf("unknown usage error=%v", err)
	}

	// 其他工作区不能引用本工作区的模型。
	_, foreign, _, _ := newAIWorkspace(t)
	_, err = agentaction.NewCreateAgentAction(db).Execute(ctx, foreign, agentaction.CreateInput{
		DisplayName: "越界引用",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: chatModelID, SystemInstruction: "回答问题",
		}},
	})
	if !errors.As(err, &validation) || validation.Fields["modelId"] != agentaction.ValidationModelInvalid {
		t.Fatalf("foreign agent error=%v", err)
	}
	if _, err := customerserviceaction.NewUpdateTranslationSettingsAction(db).Execute(ctx, foreign, &chatModelID); !errors.As(err, &validation) || validation.Fields["modelId"] != customerserviceaction.ValidationTranslationModelInvalid {
		t.Fatalf("foreign translation error=%v", err)
	}
	if options, err := list.Execute(ctx, foreign, domain.AIModelUsageAgent); err != nil || slices.ContainsFunc(options, func(option aimodel.Option) bool { return option.ID == chatModelID }) {
		t.Fatalf("foreign options=%+v err=%v", options, err)
	}
}

// TestEmbeddingModelRenameReindexesKnowledgeBases 验证在用向量模型的上游标识变化时，同一事务内重新索引引用它的知识库。
func TestEmbeddingModelRenameReindexesKnowledgeBases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	tasks := newKnowledgeTasks(t, db)
	file := uploadedDocumentFile(t, db, identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, identity, base.ID, []string{file.ID})
	if err != nil {
		t.Fatal(err)
	}
	documentID := documents[0].ID
	// processingID 读取文档当前的处理编号。
	processingID := func() string {
		var document servermodels.KnowledgeDocument
		if err := db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return document.ProcessingID
	}
	before := processingID()
	embedding := loadTestAIModel(t, db, base.EmbeddingModelID)
	provider, err := aiprovideraction.NewGetAIProviderQuery(db).Execute(ctx, identity, embedding.ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	update := aiprovideraction.NewUpdateAIProviderAction(db, knowledgeaction.NewEmbeddingReindexer(db, tasks))
	// saveProvider 以当前供应商配置保存模型目录。
	saveProvider := func(models []aiprovideraction.Model) {
		if _, err := update.Execute(ctx, identity, provider.ID, aiprovideraction.UpdateInput{
			Name: provider.Name, CredentialType: provider.CredentialType, APIKey: provider.APIKey, APIURL: provider.APIURL, Models: models,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 只改模型名称时不重新索引。
	saveProvider(modelsWith(provider.Models, base.EmbeddingModelID, func(model *aiprovideraction.Model) { model.Name = "向量 A 新名称" }))
	if processingID() != before {
		t.Fatal("renaming model display name reindexed knowledge base")
	}
	saveProvider(modelsWith(provider.Models, base.EmbeddingModelID, func(model *aiprovideraction.Model) { model.Identifier = "embedding-a-v2" }))
	after := processingID()
	if after == before {
		t.Fatal("changing embedding upstream identifier did not reindex knowledge base")
	}
	var document servermodels.KnowledgeDocument
	if err := db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if document.Status != domain.KnowledgeIndexQueued || document.SegmentBatchID != "" {
		t.Fatalf("document=%+v", document)
	}
	if count := knowledgeTaskCount(t, db, documentID, after); count != 1 {
		t.Fatalf("reindex task count=%d", count)
	}
}

// modelWith 返回按 change 修改后的模型副本。
func modelWith(model aiprovideraction.Model, change func(*aiprovideraction.Model)) aiprovideraction.Model {
	change(&model)
	return model
}

// modelsWith 返回模型目录副本，并修改指定编号的模型。
func modelsWith(models []aiprovideraction.Model, modelID string, change func(*aiprovideraction.Model)) []aiprovideraction.Model {
	result := slices.Clone(models)
	for index := range result {
		if result[index].ID == modelID {
			change(&result[index])
		}
	}
	return result
}

// knowledgeTaskCount 统计指定文档与处理编号的知识处理任务数。
func knowledgeTaskCount(t *testing.T, db bun.IDB, documentID, processingID string) int {
	t.Helper()
	count, err := db.NewSelect().TableExpr("task_runs").
		Where("payload->>'documentId' = ?", documentID).
		Where("payload->>'processingId' = ?", processingID).
		Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return count
}
