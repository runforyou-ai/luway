//go:build server

package integrationtest

import (
	"context"
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
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, err)
		return update.Execute(ctx, identity, providerID, aiprovideraction.UpdateInput{
			Name: provider.Name, CredentialType: provider.CredentialType, APIKey: provider.APIKey, APIURL: provider.APIURL, Models: models,
		})
	}
	provider, err := get.Execute(ctx, identity, providerID)
	require.NoError(t, err)
	// 新增判断模型时不传编号，由服务端分配。
	saved, err := saveModels(append(provider.Models, aiprovideraction.Model{
		Identifier: "decision-model", Name: "判断模型", Type: domain.AIModelTypeDecision,
		InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192,
	}))
	require.NoError(t, err)
	require.Len(t, saved.Models, 2)
	require.Equal(t, chatModelID, saved.Models[0].ID)
	require.True(t, str.IsUUID(saved.Models[1].ID), "saved models=%+v", saved.Models)
	decisionModelID := saved.Models[1].ID

	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "模型引用测试",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: chatModelID, SystemInstruction: "回答问题",
		}},
	})
	require.NoError(t, err)
	_, err = customerserviceaction.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: &decisionModelID, SummaryModelID: &chatModelID, Locale: domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)

	// 修改上游模型标识保留模型编号，业务引用随之使用新标识。
	renamed := slices.Clone(saved.Models)
	renamed[0].Identifier = "chat-model-v2"
	_, err = saveModels(renamed)
	require.NoError(t, err)
	loaded, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.Equal(t, chatModelID, loaded.Execution.Managed.Model.ID)
	resolved, err := aimodel.Resolve(ctx, db, identity.Workspace.ID, chatModelID, domain.AIModelUsageSummary)
	require.NoError(t, err)
	require.Len(t, resolved.Routes, 1)
	require.Equal(t, "chat-model-v2", resolved.Routes[0].Identifier)
	require.Equal(t, "test-key", resolved.Routes[0].APIKey)

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
		_, err := saveModels(models)
		require.ErrorAs(t, err, &validation, name)
		require.Equal(t, aiprovideraction.ValidationModelsInUse, validation.Fields["models"], name)
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
		_, err := saveModels(models)
		require.ErrorAs(t, err, &validation, name)
		require.Equal(t, aiprovideraction.ValidationModelsInvalid, validation.Fields["models"], name)
	}
	// 一次保存中交换两个模型的上游标识，最终目录不重复即可保存。
	swapped := []aiprovideraction.Model{
		modelWith(renamed[0], func(model *aiprovideraction.Model) { model.Identifier = renamed[1].Identifier }),
		modelWith(renamed[1], func(model *aiprovideraction.Model) { model.Identifier = renamed[0].Identifier }),
	}
	result, err := saveModels(swapped)
	require.NoError(t, err)
	require.Equal(t, chatModelID, result.Models[0].ID)
	require.Equal(t, "decision-model", result.Models[0].Identifier)
	_, err = saveModels(renamed)
	require.NoError(t, err)
	require.ErrorIs(t, aiprovideraction.NewDeleteAIProviderAction(db).Execute(ctx, identity, providerID), aiprovideraction.ErrInUse, "delete provider")

	// 模型选项按用途过滤。
	list := aimodel.NewListOptionsQuery(db)
	for usage, want := range map[domain.AIModelUsage]string{
		domain.AIModelUsageAgent: chatModelID, domain.AIModelUsageTranslation: chatModelID, domain.AIModelUsageDecision: decisionModelID,
	} {
		options, err := list.Execute(ctx, identity, usage)
		require.NoError(t, err)
		require.Len(t, options, 1, "usage=%s", usage)
		require.Equal(t, want, options[0].ID, "usage=%s", usage)
		require.Equal(t, providerID, options[0].ProviderID, "usage=%s", usage)
	}
	embeddingOptions, err := list.Execute(ctx, identity, domain.AIModelUsageEmbedding)
	require.NoError(t, err)
	require.Empty(t, embeddingOptions, "embedding options")
	_, err = list.Execute(ctx, identity, "unknown")
	require.ErrorIs(t, err, aimodel.ErrUsageInvalid, "unknown usage")

	// 其他工作区不能引用本工作区的模型。
	_, foreign, _, _ := newAIWorkspace(t)
	_, err = agentaction.NewCreateAgentAction(db).Execute(ctx, foreign, agentaction.CreateInput{
		DisplayName: "越界引用",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: chatModelID, SystemInstruction: "回答问题",
		}},
	})
	require.ErrorAs(t, err, &validation, "foreign agent error")
	require.Equal(t, agentaction.ValidationModelInvalid, validation.Fields["modelId"])
	_, err = customerserviceaction.NewUpdateTranslationSettingsAction(db).Execute(ctx, foreign, &chatModelID)
	require.ErrorAs(t, err, &validation, "foreign translation error")
	require.Equal(t, customerserviceaction.ValidationTranslationModelInvalid, validation.Fields["modelId"])
	foreignOptions, err := list.Execute(ctx, foreign, domain.AIModelUsageAgent)
	require.NoError(t, err)
	require.False(t, slices.ContainsFunc(foreignOptions, func(option aimodel.Option) bool { return option.ID == chatModelID }), "foreign options=%+v", foreignOptions)
}

// TestEmbeddingModelRenameReindexesKnowledgeBases 验证在用向量模型的上游标识变化时，同一事务内重新索引引用它的知识库。
func TestEmbeddingModelRenameReindexesKnowledgeBases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	identity := installed.Identity
	tasks := newKnowledgeTasks(t, db)
	file := uploadedDocumentFile(t, db, identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, identity, base.ID, []string{file.ID})
	require.NoError(t, err)
	documentID := documents[0].ID
	// processingID 读取文档当前的处理编号。
	processingID := func() string {
		var document servermodels.KnowledgeDocument
		require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx))
		return document.ProcessingID
	}
	before := processingID()
	embedding := loadTestAIModel(t, db, base.EmbeddingModelID)
	provider, err := aiprovideraction.NewGetAIProviderQuery(db).Execute(ctx, identity, embedding.ProviderID)
	require.NoError(t, err)
	update := aiprovideraction.NewUpdateAIProviderAction(db, knowledgeaction.NewEmbeddingReindexer(db, tasks))
	// saveProvider 以当前供应商配置保存模型目录。
	saveProvider := func(models []aiprovideraction.Model) {
		_, err := update.Execute(ctx, identity, provider.ID, aiprovideraction.UpdateInput{
			Name: provider.Name, CredentialType: provider.CredentialType, APIKey: provider.APIKey, APIURL: provider.APIURL, Models: models,
		})
		require.NoError(t, err)
	}

	// 只改模型名称时不重新索引。
	saveProvider(modelsWith(provider.Models, base.EmbeddingModelID, func(model *aiprovideraction.Model) { model.Name = "向量 A 新名称" }))
	require.Equal(t, before, processingID(), "renaming model display name reindexed knowledge base")
	saveProvider(modelsWith(provider.Models, base.EmbeddingModelID, func(model *aiprovideraction.Model) { model.Identifier = "embedding-a-v2" }))
	after := processingID()
	require.NotEqual(t, before, after, "changing embedding upstream identifier did not reindex knowledge base")
	var document servermodels.KnowledgeDocument
	require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx))
	require.Equal(t, domain.KnowledgeIndexQueued, document.Status)
	require.Empty(t, document.SegmentBatchID)
	require.Equal(t, 1, knowledgeTaskCount(t, tasks, documentID, after), "reindex task count")
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

// knowledgeTaskCount 统计登记器中指定文档与处理编号的知识处理任务数。
func knowledgeTaskCount(t *testing.T, tasks *servertest.Tasks, documentID, processingID string) int {
	t.Helper()
	return len(servertest.QueuedInputs(t, tasks, knowledgeaction.ProcessDocumentActionName, func(input knowledgeaction.ProcessInput) bool {
		return input.DocumentID == documentID && input.ProcessingID == processingID
	}))
}
