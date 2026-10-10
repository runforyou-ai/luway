//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// newQAFixture 创建独立测试工作区和本地问答库。
func newQAFixture(t *testing.T, db *bun.DB) (*servermodels.Identity, *knowledgeaction.Record) {
	t.Helper()
	ctx := context.Background()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "问答测试", DisplayName: "维护人员", Email: servertest.UniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, installed.Identity, newKnowledgeBaseInput(t, db, installed.Identity, "FAQ", domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	return installed.Identity, base
}

// TestKnowledgeQALifecycle 验证问答内容编号、知识库内分页搜索和删除一致性。
func TestKnowledgeQALifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	save := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db))
	list := knowledgeaction.NewListQAEntriesQuery(db)
	get := knowledgeaction.NewGetQAEntryQuery(db)
	remove := knowledgeaction.NewDeleteQAEntryAction(db)
	input := knowledgeaction.QAInput{Question: "  如何退款？  ", Answer: "  进入订单详情。  ", SimilarQuestions: []knowledgeaction.QASimilarQuestion{
		{Content: "退款入口"}, {Content: "退款入口"}, {Content: " 如何退款？ "}, {Content: " "}, {Content: "退还100%费用"},
	}}
	created, err := save.Execute(ctx, identity, base.ID, "", input)
	require.NoError(t, err)
	require.Equal(t, "如何退款？", created.Question)
	require.Equal(t, "进入订单详情。", created.Answer)
	require.Len(t, created.SimilarQuestions, 2)
	var original []servermodels.KnowledgeQAContent
	require.NoError(t, db.NewSelect().Model(&original).Where("entry_id = ?", created.ID).Scan(ctx))
	input.Question, input.Answer = created.Question, created.Answer
	input.SimilarQuestions = []knowledgeaction.QASimilarQuestion{created.SimilarQuestions[1], created.SimilarQuestions[0]}
	input.SimilarQuestions[1].Content = "退款办理入口"
	updated, err := save.Execute(ctx, identity, base.ID, created.ID, input)
	require.NoError(t, err)
	require.Equal(t, created.SimilarQuestions[1].ID, updated.SimilarQuestions[0].ID)
	require.Equal(t, created.SimilarQuestions[0].ID, updated.SimilarQuestions[1].ID)
	var current []servermodels.KnowledgeQAContent
	require.NoError(t, db.NewSelect().Model(&current).Where("entry_id = ?", created.ID).Scan(ctx))
	for _, before := range original {
		found := false
		for _, after := range current {
			if before.ID != after.ID {
				continue
			}
			found = true
			if before.Kind != domain.KnowledgeQAContentSimilarQuestion {
				require.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "unchanged content timestamp changed")
			}
		}
		require.True(t, found, "content ID replaced: %s", before.ID)
	}
	// 相似问题搜索命中整条问答，百分号按字面匹配，答案不参与问题搜索。
	for keyword, total := range map[string]int{"退款": 1, "100%": 1, "费用": 1, "进入订单": 0, "_": 0} {
		page, err := list.Execute(ctx, identity, base.ID, knowledgeaction.QAListInput{Keyword: keyword, Page: 1, PageSize: 1})
		require.NoError(t, err, "query=%q", keyword)
		require.Equal(t, total, page.Total, "query=%q", keyword)
		require.Len(t, page.Entries, total, "query=%q", keyword)
		if total == 1 {
			entry := page.Entries[0]
			require.Equal(t, created.ID, entry.ID)
			require.Equal(t, updated.Answer, entry.Answer)
			require.Equal(t, []string{updated.SimilarQuestions[0].Content, updated.SimilarQuestions[1].Content}, entry.SimilarQuestions)
		}
	}
	_, err = knowledgeaction.NewUpdateKnowledgeBaseAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, base.ID, newKnowledgeBaseInput(t, db, identity, base.Name, domain.KnowledgeBaseCategoryStandard))
	require.ErrorIs(t, err, knowledgeaction.ErrBaseHasContent, "change type")
	input.SimilarQuestions = []knowledgeaction.QASimilarQuestion{{Content: "新增相似问题"}}
	updated, err = save.Execute(ctx, identity, base.ID, created.ID, input)
	require.NoError(t, err)
	exists, err := db.NewSelect().Model((*servermodels.KnowledgeQAContent)(nil)).Where("id = ?", created.SimilarQuestions[0].ID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "removed content remains")
	detail, err := get.Execute(ctx, identity, base.ID, updated.ID)
	require.NoError(t, err)
	require.Equal(t, updated.SimilarQuestions[0].ID, detail.SimilarQuestions[0].ID)
	// 添加第二条问答后检查分页总数和不重叠的条目。
	another, err := save.Execute(ctx, identity, base.ID, "", knowledgeaction.QAInput{Question: "另一个问题", Answer: "另一个答案"})
	require.NoError(t, err)
	first, err := list.Execute(ctx, identity, base.ID, knowledgeaction.QAListInput{Page: 1, PageSize: 1})
	require.NoError(t, err)
	second, err := list.Execute(ctx, identity, base.ID, knowledgeaction.QAListInput{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, 2, first.Total)
	require.Equal(t, 2, second.Total)
	require.Len(t, second.Entries, 1)
	require.NotEqual(t, second.Entries[0].ID, first.Entries[0].ID)
	require.Equal(t, another.ID, first.Entries[0].ID)
	require.Empty(t, first.Entries[0].SimilarQuestions)
	require.Equal(t, another.Answer, first.Entries[0].Answer)
	require.NoError(t, remove.Execute(ctx, identity, base.ID, created.ID))
	_, err = get.Execute(ctx, identity, base.ID, created.ID)
	require.ErrorIs(t, err, knowledgeaction.ErrQANotFound, "deleted entry")
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, base.ID))
	count, err := db.NewSelect().Model((*servermodels.KnowledgeQAContent)(nil)).Where("entry_id IN (?, ?)", created.ID, another.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "orphan contents")
	count, err = db.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Where("knowledge_base_id = ?", base.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "orphan entries")
}

// TestKnowledgeQAIsolation 验证企业、知识库和内容编号边界，以及失败保存的事务回滚。
func TestKnowledgeQAIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity, base := newQAFixture(t, db)
	foreignIdentity, foreignBase := newQAFixture(t, db)
	save := knowledgeaction.NewSaveQAEntryAction(db, newKnowledgeTasks(t, db))
	get := knowledgeaction.NewGetQAEntryQuery(db)
	input := knowledgeaction.QAInput{Question: "问题", Answer: "答案", SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "相似问题"}}}
	entry, err := save.Execute(ctx, identity, base.ID, "", input)
	require.NoError(t, err)
	_, err = get.Execute(ctx, foreignIdentity, base.ID, entry.ID)
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign read")
	_, err = save.Execute(ctx, foreignIdentity, base.ID, entry.ID, input)
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign update")
	require.ErrorIs(t, knowledgeaction.NewDeleteQAEntryAction(db).Execute(ctx, foreignIdentity, base.ID, entry.ID), knowledgeaction.ErrNotFound, "foreign delete")
	_, err = knowledgeaction.NewListQAEntriesQuery(db).Execute(ctx, foreignIdentity, base.ID, knowledgeaction.QAListInput{})
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign list")
	_, err = save.Execute(ctx, identity, foreignBase.ID, "", input)
	require.ErrorIs(t, err, knowledgeaction.ErrNotFound, "foreign base")
	sameOrgBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "其他FAQ", domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	_, err = get.Execute(ctx, identity, sameOrgBase.ID, entry.ID)
	require.ErrorIs(t, err, knowledgeaction.ErrQANotFound, "other base read")
	_, err = save.Execute(ctx, identity, sameOrgBase.ID, entry.ID, input)
	require.ErrorIs(t, err, knowledgeaction.ErrQANotFound, "other base update")
	page, err := knowledgeaction.NewListQAEntriesQuery(db).Execute(ctx, identity, sameOrgBase.ID, knowledgeaction.QAListInput{})
	require.NoError(t, err, "other base list")
	require.Zero(t, page.Total, "other base list")
	input.SimilarQuestions = entry.SimilarQuestions
	// 核验内容编号的条目归属及失败后的事务回滚。
	_, err = save.Execute(ctx, identity, base.ID, "", input)
	require.Error(t, err, "accepted content from another entry")
	count, err := db.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Where("knowledge_base_id = ?", base.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "failed insert not rolled back")
	input.Question = "不应保存"
	input.SimilarQuestions = []knowledgeaction.QASimilarQuestion{{ID: uuid.NewV7().String(), Content: "无效编号"}}
	_, err = save.Execute(ctx, identity, base.ID, entry.ID, input)
	require.Error(t, err, "accepted nonexistent content")
	record, err := get.Execute(ctx, identity, base.ID, entry.ID)
	require.NoError(t, err)
	require.Equal(t, entry.Question, record.Question, "failed update not rolled back")
	require.True(t, record.UpdatedAt.Equal(entry.UpdatedAt), "failed update not rolled back")
	standard, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "文档库", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	input.Question = "问题"
	_, err = save.Execute(ctx, identity, standard.ID, "", input)
	require.ErrorIs(t, err, knowledgeaction.ErrQAUnsupported, "standard base")
}
