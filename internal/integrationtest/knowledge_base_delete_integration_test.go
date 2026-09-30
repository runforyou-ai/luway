//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestKnowledgeBaseDeleteWaitsForQAPublish 验证删除知识库等待进行中的问答发布事务，发布提交后不残留分段。
func TestKnowledgeBaseDeleteWaitsForQAPublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	entry, err := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "如何退款？", Answer: "进入订单详情申请退款。"})
	if err != nil {
		t.Fatal(err)
	}

	// 模拟问答发布事务：持有条目行锁并写入分段，暂不提交。
	publish, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publish.Rollback()
	if _, err := publish.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Column("id").Where("kqe.id = ?", entry.ID).For("UPDATE").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.NewRaw("INSERT INTO public.knowledge_segments (id, organization_id, knowledge_base_id, source_type, source_id, segment_batch_id, position, character_count, context, content) VALUES (?, ?, ?, ?, ?, ?, 1, 2, '', '退款')",
		uuid.NewV7().String(), identity.Organization.ID, base.ID, domain.KnowledgeSourceQAEntry, entry.ID, uuid.NewV7().String()).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	deleted := make(chan error, 1)
	go func() {
		deleted <- knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID)
	}()
	// 等待删除事务阻塞在问答条目行锁上。
	blocked := false
	for range 100 {
		count, err := db.NewSelect().TableExpr("pg_stat_activity").Where("wait_event_type = 'Lock' AND query LIKE ?", "%knowledge_qa_entries%"+base.ID+"%FOR UPDATE%").Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			blocked = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("删除知识库未等待问答条目行锁")
	}
	if err := publish.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	remaining, err := db.NewSelect().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", base.ID).Count(ctx)
	if err != nil || remaining != 0 {
		t.Fatalf("remaining=%d %v", remaining, err)
	}
}
