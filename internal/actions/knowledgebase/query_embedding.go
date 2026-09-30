//go:build server

package knowledgebase

import (
	"context"
	"fmt"
	"sync"

	"github.com/runforyou-ai/cervi/pkg/embedding"
)

// queryEmbeddingKey 标识一组可以共用查询向量的向量模型配置。
type queryEmbeddingKey struct {
	providerID string
	model      string
	dimension  int
}

// queryEmbedding 保存一条查询的向量化结果，done 关闭后 vector 与 err 可读。
type queryEmbedding struct {
	done   chan struct{}
	vector []float32
	err    error
}

// queryEmbeddings 在一次检索内为同一向量模型配置下的全部知识库批量向量化查询，并按查询文本共享结果。
type queryEmbeddings struct {
	embedder   queryEmbedder
	credential embedding.Credential
	key        queryEmbeddingKey

	mu      sync.Mutex
	results map[string]*queryEmbedding
}

// newQueryEmbeddings 创建一组向量模型配置的查询向量缓存。
func newQueryEmbeddings(embedder queryEmbedder, credential embedding.Credential, key queryEmbeddingKey) *queryEmbeddings {
	return &queryEmbeddings{embedder: embedder, credential: credential, key: key, results: map[string]*queryEmbedding{}}
}

// submit 按输入顺序返回各查询的向量化结果；尚未提交的查询以一次模型调用在后台向量化，已提交的查询复用原结果，失败结果同样复用。
func (q *queryEmbeddings) submit(ctx context.Context, queries []string) []*queryEmbedding {
	q.mu.Lock()
	results := make([]*queryEmbedding, len(queries))
	batch := make([]string, 0, len(queries))
	pending := make([]*queryEmbedding, 0, len(queries))
	for index, query := range queries {
		result, exists := q.results[query]
		if !exists {
			result = &queryEmbedding{done: make(chan struct{})}
			q.results[query] = result
			batch, pending = append(batch, query), append(pending, result)
		}
		results[index] = result
	}
	q.mu.Unlock()
	if len(batch) == 0 {
		return results
	}
	go func() {
		vectors, err := q.embedder.Embed(ctx, q.credential, q.key.model, q.key.dimension, batch)
		if err == nil && len(vectors) != len(batch) {
			err = fmt.Errorf("embedding returned %d vectors for %d queries", len(vectors), len(batch))
		}
		for index, result := range pending {
			if err != nil {
				result.err = err
			} else {
				result.vector = vectors[index]
			}
			close(result.done)
		}
	}()
	return results
}

// vector 等待并返回查询向量，尚未提交的查询先单独提交。
func (q *queryEmbeddings) vector(ctx context.Context, query string) ([]float32, error) {
	result := q.submit(ctx, []string{query})[0]
	select {
	case <-result.done:
		return result.vector, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
