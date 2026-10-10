//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	"github.com/stretchr/testify/require"

	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
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
	require.NoError(t, err)
	length, overlap := 512, 50
	input := knowledgeaction.Input{Name: name, Category: category, EmbeddingModelID: aiModelID(t, db, provider.ID, "embedding-a"), EmbeddingDimension: 1024, RetrievalCount: 3, RetrievalScoreThreshold: 0.7, RerankModelID: aiModelID(t, db, provider.ID, "rerank")}
	if category == domain.KnowledgeBaseCategoryStandard {
		input.ChunkLength, input.ChunkOverlap = &length, &overlap
	}
	return input
}
