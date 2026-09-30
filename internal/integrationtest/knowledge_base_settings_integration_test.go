//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// newKnowledgeBaseInput 创建当前企业的模型目录和完整知识库测试配置。
func newKnowledgeBaseInput(t *testing.T, db *bun.DB, identity *servermodels.Identity, name string, category domain.KnowledgeBaseCategory) knowledgeaction.Input {
	t.Helper()
	provider, err := aiprovideraction.NewCreateAIProviderAction(db).Execute(context.Background(), identity, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{
			{Identifier: "embedding-a", Name: "向量 A", Type: domain.AIModelTypeEmbedding, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192},
			{Identifier: "embedding-b", Name: "向量 B", Type: domain.AIModelTypeEmbedding, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192},
			{Identifier: "rerank", Name: "重排", Type: domain.AIModelTypeRerank, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	length, overlap := 512, 50
	input := knowledgeaction.Input{Name: name, Category: category, EmbeddingProviderID: provider.ID, EmbeddingModelIdentifier: "embedding-a", EmbeddingDimension: 1024, RetrievalCount: 3, RetrievalScoreThreshold: 0.7, RerankProviderID: provider.ID, RerankModelIdentifier: "rerank"}
	if category == domain.KnowledgeBaseCategoryStandard {
		input.ChunkLength, input.ChunkOverlap = &length, &overlap
	}
	return input
}

// TestKnowledgeBaseSettings 验证独立保存、问答字段、模型用途、企业边界及引用保护。
func TestKnowledgeBaseSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	identity, qa := newQAFixture(t, db)
	input := newKnowledgeBaseInput(t, db, identity, "配置测试", domain.KnowledgeBaseCategoryStandard)
	create, update := knowledgeaction.NewCreateKnowledgeBaseAction(db), knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db))
	base, err := create.Execute(ctx, identity, input)
	if err != nil {
		t.Fatal(err)
	}
	if base.EmbeddingDimension != 1024 || *base.ChunkLength != 512 || *base.ChunkOverlap != 50 || base.RetrievalCount != 3 || base.RetrievalScoreThreshold != 0.7 || base.RerankModelIdentifier != "rerank" {
		t.Fatalf("base=%+v", base)
	}
	input.EmbeddingModelIdentifier, input.EmbeddingDimension, input.RetrievalCount, input.RetrievalScoreThreshold = "embedding-b", 768, 20, 0.35
	updated, err := update.Execute(ctx, identity, base.ID, input)
	if err != nil || updated.EmbeddingModelIdentifier != "embedding-b" || updated.EmbeddingDimension != 768 || updated.RetrievalCount != 20 || updated.RetrievalScoreThreshold != 0.35 || updated.RerankModelIdentifier != "rerank" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	loaded, err := knowledgeaction.NewGetKnowledgeBaseQuery(db).Execute(ctx, identity, base.ID)
	if err != nil || loaded.EmbeddingDimension != 768 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	other, err := knowledgeaction.NewGetKnowledgeBaseQuery(db).Execute(ctx, identity, qa.ID)
	if err != nil || other.EmbeddingDimension != 1024 || other.RetrievalCount != 3 || other.ChunkLength != nil || other.ChunkOverlap != nil {
		t.Fatalf("other=%+v err=%v", other, err)
	}
	// 问答库不保留客户端传入的分段值。
	input.Category = domain.KnowledgeBaseCategoryQA
	updated, err = update.Execute(ctx, identity, base.ID, input)
	if err != nil || updated.ChunkLength != nil || updated.ChunkOverlap != nil {
		t.Fatalf("qa=%+v err=%v", updated, err)
	}
	var fieldError *common.FieldError
	foreignIdentity, _ := newQAFixture(t, db)
	_, err = create.Execute(ctx, foreignIdentity, input)
	if !errors.As(err, &fieldError) || fieldError.Fields["embeddingModelIdentifier"] != knowledgeaction.ValidationEmbeddingModelInvalid {
		t.Fatalf("foreign model error=%v", err)
	}
	input.EmbeddingModelIdentifier = "rerank"
	_, err = update.Execute(ctx, identity, base.ID, input)
	if !errors.As(err, &fieldError) || fieldError.Fields["embeddingModelIdentifier"] != knowledgeaction.ValidationEmbeddingModelInvalid {
		t.Fatalf("model type error=%v", err)
	}
	input.EmbeddingModelIdentifier = "embedding-b"
	input.RerankProviderID, input.RerankModelIdentifier = input.EmbeddingProviderID, "embedding-a"
	_, err = update.Execute(ctx, identity, base.ID, input)
	if !errors.As(err, &fieldError) || fieldError.Fields["rerankModelIdentifier"] != knowledgeaction.ValidationRerankModelInvalid {
		t.Fatalf("rerank type error=%v", err)
	}
	if err := aiprovideraction.NewDeleteAIProviderAction(db).Execute(ctx, identity, input.EmbeddingProviderID); !errors.Is(err, aiprovideraction.ErrInUse) {
		t.Fatalf("delete provider error=%v", err)
	}
	provider, err := aiprovideraction.NewGetAIProviderQuery(db).Execute(ctx, identity, input.EmbeddingProviderID)
	if err != nil {
		t.Fatal(err)
	}
	// 按标识移除被引用模型。
	provider.Models = slices.DeleteFunc(provider.Models, func(model aiprovideraction.Model) bool {
		return model.Identifier == "embedding-b"
	})
	_, err = aiprovideraction.NewUpdateAIProviderAction(db).Execute(ctx, identity, provider.ID, aiprovideraction.UpdateInput{Name: provider.Name, CredentialType: provider.CredentialType, APIKey: provider.APIKey, APIURL: provider.APIURL, Models: provider.Models})
	var validation *aiprovideraction.ValidationError
	if !errors.As(err, &validation) || validation.Fields["models"] != aiprovideraction.ValidationModelsInUse {
		t.Fatalf("remove model error=%v", err)
	}
}
