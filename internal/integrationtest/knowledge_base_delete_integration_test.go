//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestKnowledgeBaseDeleteWaitsForQAPublish 验证删除知识库等待进行中的问答发布事务，发布提交后不残留分段。
func TestKnowledgeBaseDeleteWaitsForQAPublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: "进入订单详情申请退款。"})
	require.NoError(t, err)

	// 模拟问答发布事务：持有条目行锁并写入分段，暂不提交。
	publish, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer publish.Rollback()
	_, err = publish.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Column("id").Where("kqe.id = ?", entry.ID).For("UPDATE").Exec(ctx)
	require.NoError(t, err)
	_, err = publish.NewRaw("INSERT INTO public.knowledge_segments (id, workspace_id, knowledge_base_id, source_type, source_id, segment_batch_id, position, character_count, context, content) VALUES (?, ?, ?, ?, ?, ?, 1, 2, '', '退款')",
		uuid.NewV7().String(), identity.Workspace.ID, base.ID, domain.KnowledgeSourceQAEntry, entry.ID, uuid.NewV7().String()).Exec(ctx)
	require.NoError(t, err)

	deleted := make(chan error, 1)
	go func() {
		deleted <- knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID)
	}()
	// 等待删除事务阻塞在问答条目行锁上。
	blocked := false
	for range 100 {
		count, err := db.NewSelect().TableExpr("pg_stat_activity").Where("wait_event_type = 'Lock' AND query LIKE ?", "%knowledge_qa_entries%"+base.ID+"%FOR UPDATE%").Count(ctx)
		require.NoError(t, err)
		if count > 0 {
			blocked = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, blocked, "删除知识库未等待问答条目行锁")
	require.NoError(t, publish.Commit())
	require.NoError(t, <-deleted)
	remaining, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", base.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, remaining)
}
