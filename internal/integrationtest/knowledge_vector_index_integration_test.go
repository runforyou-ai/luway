//go:build server

package integrationtest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/stretchr/testify/require"
)

// TestReconcileKnowledgeVectorIndexes 验证已发布分段达到阈值的知识库获得专属向量索引，维度变化时重建，知识库删除后移除，上下文取消时中断等待中的索引构建。
func TestReconcileKnowledgeVectorIndexes(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	reconcile := knowledgeaction.NewReconcileVectorIndexesAction(f.db)
	knowledgeBaseID, small := uuid.NewV7().String(), uuid.NewV7().String()
	for _, item := range []struct {
		id       string
		segments int
	}{{knowledgeBaseID, 10000}, {small, 9999}} {
		_, err := f.db.NewRaw(`INSERT INTO knowledge_bases (id, workspace_id, created_by_user_id, name, category, retrieval_score_threshold, rerank_model_id, embedding_model_id, embedding_dimension)
			VALUES (?, ?, ?, ?, 'standard', 0.5, ?, ?, 1024)`, item.id, f.owner.Workspace.ID, f.owner.User.ID, "索引对账"+item.id, uuid.NewV7().String(), uuid.NewV7().String()).Exec(ctx)
		require.NoError(t, err)
		_, err = f.db.NewRaw(`INSERT INTO knowledge_documents (knowledge_base_id, created_by_user_id, segment_count) VALUES (?, ?, ?)`,
			item.id, f.owner.User.ID, item.segments).Exec(ctx)
		require.NoError(t, err)
	}
	// 对账后读取两个测试知识库的专属索引，无效索引在名称后标注 invalid。
	indexes := func() []string {
		t.Helper()
		require.NoError(t, reconcile.Execute(ctx, knowledgeaction.ReconcileVectorIndexesInput{}))
		var names []string
		require.NoError(t, f.db.NewRaw(`SELECT c.relname || CASE WHEN i.indisvalid THEN '' ELSE ' invalid' END
			FROM pg_index AS i JOIN pg_class AS c ON c.oid = i.indexrelid
			WHERE i.indrelid = 'public.knowledge_segments'::regclass AND c.relname LIKE ?`,
			"knowledge_segments_hnsw_%").Scan(ctx, &names))
		return slices.DeleteFunc(names, func(name string) bool {
			return !strings.Contains(name, strings.ReplaceAll(knowledgeBaseID, "-", "")) && !strings.Contains(name, strings.ReplaceAll(small, "-", ""))
		})
	}
	// 反复对账直到专属索引与预期一致：被取消的对账连接释放咨询锁之前，新一轮对账按设计跳过。
	waitIndexes := func(want []string) []string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			names := indexes()
			if slices.Equal(names, want) || time.Now().After(deadline) {
				return names
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	prefix := "knowledge_segments_hnsw_" + strings.ReplaceAll(knowledgeBaseID, "-", "")
	require.Equal(t, []string{prefix + "_1024"}, waitIndexes([]string{prefix + "_1024"}), "达到阈值后的专属索引")
	_, err := f.db.NewRaw("UPDATE knowledge_bases SET embedding_dimension = 768 WHERE id = ?", knowledgeBaseID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{prefix + "_768"}, waitIndexes([]string{prefix + "_768"}), "维度变化后的专属索引")
	_, err = f.db.NewRaw("DELETE FROM knowledge_bases WHERE id = ?", knowledgeBaseID).Exec(ctx)
	require.NoError(t, err)
	require.Empty(t, waitIndexes(nil), "知识库删除后的专属索引")

	// 未结束的写事务使并发建索引持续等待，对账上下文到期后应立即中断语句。
	_, err = f.db.NewRaw("UPDATE knowledge_bases SET embedding_dimension = 1024 WHERE id = ?", small).Exec(ctx)
	require.NoError(t, err)
	_, err = f.db.NewRaw("UPDATE knowledge_documents SET segment_count = 10000 WHERE knowledge_base_id = ?", small).Exec(ctx)
	require.NoError(t, err)
	writer, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer writer.Rollback()
	_, err = writer.NewRaw("UPDATE knowledge_segments SET updated_at = now() WHERE false").Exec(ctx)
	require.NoError(t, err)
	cancelCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	require.Error(t, reconcile.Execute(cancelCtx, knowledgeaction.ReconcileVectorIndexesInput{}), "等待中的索引构建在上下文到期后未返回错误")
	require.LessOrEqual(t, time.Since(started), 5*time.Second, "上下文到期后索引构建过慢返回")
	require.NoError(t, writer.Rollback())
	smallIndex := []string{"knowledge_segments_hnsw_" + strings.ReplaceAll(small, "-", "") + "_1024"}
	require.Equal(t, smallIndex, waitIndexes(smallIndex), "中断后重新对账的专属索引")
	_, err = f.db.NewRaw("DELETE FROM knowledge_bases WHERE id = ?", small).Exec(ctx)
	require.NoError(t, err)
	require.Empty(t, waitIndexes(nil), "清理后的专属索引")
}
