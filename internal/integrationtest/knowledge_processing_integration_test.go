//go:build server

package integrationtest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/runforyou-ai/einorun/provider/apierr"
	"github.com/runforyou-ai/einorun/provider/embedding"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/documentconvert"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// processingProbe 是知识文档处理测试的转换、网页与向量替身。
type processingProbe struct {
	fail       bool
	embedFail  bool
	credential embedding.Endpoint
	markdown   string
	fetchErr   error
}

// Fetch 返回预设的网页内容，抓取失败时返回预设错误。
func (p *processingProbe) Fetch(context.Context, string) (webfetch.Page, error) {
	if p.fetchErr != nil {
		return webfetch.Page{}, p.fetchErr
	}
	return webfetch.Page{ContentType: webfetch.ContentTypeHTML}, nil
}

// Open 为执行任务提供固定原件。
func (p *processingProbe) Open(context.Context, *servermodels.File) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("原件")), nil
}

// Convert 返回预设正文，或按需模拟转换失败。
func (p *processingProbe) Convert(context.Context, string, string, io.Reader) (string, error) {
	if p.fail {
		return "", &documentconvert.Error{Code: "parse_failed"}
	}
	if p.markdown != "" {
		return p.markdown, nil
	}
	return "正文", nil
}

// Embed 记录本次凭据并按维度返回定长向量，或按需模拟向量接口失败。
func (p *processingProbe) Embed(_ context.Context, credential embedding.Endpoint, _ string, dimension int, inputs []string) (embedding.Result, error) {
	p.credential = credential
	if p.embedFail {
		return embedding.Result{}, apierr.FromStatus(http.StatusInternalServerError, "")
	}
	vectors := make([][]float32, len(inputs))
	for index := range vectors {
		vectors[index] = make([]float32, dimension)
	}
	return embedding.Result{Vectors: vectors}, nil
}

// TestKnowledgeProcessingRetryAndPublication 验证上传投递、失败重试幂等、参数快照与完整发布。
func TestKnowledgeProcessingRetryAndPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, base := newDocumentFixture(t, db)
	tasks := newKnowledgeTasks(t, db)
	file := uploadedDocumentFile(t, db, installed.Identity, "资料.txt")
	documents, err := knowledgeaction.NewCreateDocumentsAction(db, tasks).Execute(ctx, installed.Identity, base.ID, []string{file.ID})
	require.NoError(t, err)
	documentID := documents[0].ID
	var document servermodels.KnowledgeDocument
	require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx))
	require.Equal(t, domain.KnowledgeIndexQueued, document.Status)
	require.Equal(t, 512, document.ChunkLength)
	require.NotEmpty(t, document.ProcessingID)
	input := knowledgeaction.ProcessInput{WorkspaceID: installed.Identity.Workspace.ID, KnowledgeBaseID: base.ID, DocumentID: documentID, ProcessingID: document.ProcessingID, ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap, EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension}
	probe := &processingProbe{fail: true}
	worker := knowledgeaction.NewProcessDocumentAction(db, probe, modelcall.New(db, modelcall.Upstreams{Embedder: probe}, nil), probe, probe)
	err = worker.Execute(ctx, input)
	require.Error(t, err)
	require.NoError(t, worker.FinalizeFailure(ctx, input, err))
	query := knowledgeaction.NewDocumentQuery(db)
	failed, err := query.Get(ctx, installed.Identity, base.ID, documentID)
	require.NoError(t, err)
	require.Equal(t, domain.KnowledgeIndexFailed, failed.Status)
	require.Equal(t, "parse_failed", failed.FailureCode)
	// 核验连续重试后生效的任务标识。
	retry := knowledgeaction.NewDocumentProcessing(db, tasks)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			assert.NoError(t, retry.Retry(ctx, installed.Identity, base.ID, documentID))
		})
	}
	group.Wait()
	require.NoError(t, db.NewSelect().Model(&document).Where("kd.id = ?", documentID).Scan(ctx))
	require.NotEqual(t, input.ProcessingID, document.ProcessingID, "retry reused failed execution")
	documentTasks := servertest.QueuedInputs(t, tasks, knowledgeaction.ProcessDocumentActionName, func(input knowledgeaction.ProcessInput) bool { return input.DocumentID == documentID })
	require.Len(t, documentTasks, 3)
	// 核验旧任务成功和失败后的当前任务状态。
	require.NoError(t, worker.Execute(ctx, input))
	require.NoError(t, worker.FinalizeFailure(ctx, input, errors.New("old failure")))
	input.ProcessingID = document.ProcessingID
	probe.fail = false
	require.NoError(t, worker.Execute(ctx, input))
	require.NoError(t, worker.Execute(ctx, input))
	completed, err := query.Get(ctx, installed.Identity, base.ID, documentID)
	require.NoError(t, err)
	require.Equal(t, domain.KnowledgeIndexSucceeded, completed.Status)
	require.Equal(t, 1, completed.SegmentCount)
	require.Equal(t, input.ProcessingID, completed.SegmentBatchID)
	// 核验任务载荷的向量配置快照、执行时解析的模型凭据和落库的分段维度。
	snapshots := servertest.QueuedInputs(t, tasks, knowledgeaction.ProcessDocumentActionName, func(input knowledgeaction.ProcessInput) bool {
		return input.DocumentID == documentID && input.EmbeddingModelID == base.EmbeddingModelID && input.EmbeddingDimension == 1024
	})
	require.NotEmpty(t, snapshots)
	require.Equal(t, "https://models.test/v1", probe.credential.BaseURL)
	require.Equal(t, "test-key", probe.credential.APIKey)
	stored, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ? AND embedding_dimension = 1024 AND embedding IS NOT NULL", documentID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stored)
	require.NoError(t, retry.Retry(ctx, installed.Identity, base.ID, documentID))
	require.NoError(t, knowledgeaction.NewDeleteDocumentAction(db).Execute(ctx, installed.Identity, base.ID, documentID))
	count, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("source_id = ?", documentID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, worker.Execute(ctx, input))
}
