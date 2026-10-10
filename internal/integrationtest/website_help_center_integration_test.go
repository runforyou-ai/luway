//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	helpcenteraction "github.com/runforyou-ai/luway/internal/actions/helpcenter"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestWebsiteHelpCenter 验证网站渠道帮助中心的发布范围、合集与文章读取、只检索已发布文章、渠道停用与知识库删除。
func TestWebsiteHelpCenter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, documentBase := newDocumentFixture(t, db)
	identity := installed.Identity
	qaBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "常见问题", domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	internalBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "内部资料", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	probe := &retrievalProbe{}
	text, err := knowledgeaction.NewSaveTextDocumentAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, documentBase.ID, "", knowledgeaction.TextDocumentInput{Title: "退款说明", Content: "# 退款\n\n签收后七天内可以申请退款。"})
	require.NoError(t, err)
	runDocumentProcessing(t, db, probe, identity.Workspace.ID, documentBase.ID, text.ID, false)
	fileDocumentID := publishRetrievalDocument(t, db, probe, identity, documentBase, "退款内部流程.txt", "退款审批由财务复核。")
	internalDocumentID := publishRetrievalDocument(t, db, probe, identity, internalBase, "退款底线.txt", "退款最多补偿五十元。")
	qaEntryID := publishQAEntry(t, db, probe, identity, qaBase, knowledgeaction.QAInput{Question: "如何开发票？", Answer: "下单时选择电子发票。"})

	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "帮助中心验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	getHelpCenter := helpcenteraction.NewGetHelpCenterQuery(db)
	getArticle := helpcenteraction.NewGetArticleQuery(db)
	search := helpcenteraction.NewSearchQuery(db, knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil)))

	// 未发布任何知识库时没有合集，搜索不返回结果。
	collections, err := getHelpCenter.Execute(ctx, channel.ID)
	require.NoError(t, err)
	require.Empty(t, collections)

	// 发布范围去重后按知识库名称排序，不存在的知识库被拒绝。
	update := channelaction.NewUpdateWebsiteChannelHelpCenterAction(db)
	_, err = update.Execute(ctx, identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{KnowledgeBaseIDs: []string{uuid.NewV7().String()}})
	require.ErrorAs(t, err, new(*common.FieldError))
	other, _ := newDocumentFixture(t, db)
	_, err = update.Execute(ctx, other.Identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{})
	require.ErrorIs(t, err, channelaction.ErrNotFound)
	published, err := update.Execute(ctx, identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{Enabled: false, KnowledgeBaseIDs: []string{qaBase.ID, documentBase.ID, qaBase.ID}})
	require.NoError(t, err)
	require.False(t, published.Enabled)
	require.Equal(t, []string{qaBase.ID, documentBase.ID}, published.KnowledgeBaseIDs)
	detail, err := channelaction.NewGetWebsiteChannelQuery(db).Execute(ctx, identity, channel.ID)
	require.NoError(t, err)
	require.False(t, detail.ChatInterface.HelpEnabled)
	require.Equal(t, published.KnowledgeBaseIDs, detail.HelpCenterKnowledgeBaseIDs)

	// 合集只包含在线编写的文档与问答条目。
	collections, err = getHelpCenter.Execute(ctx, channel.ID)
	require.NoError(t, err)
	require.Len(t, collections, 2)
	require.Equal(t, qaBase.ID, collections[0].ID)
	require.Equal(t, []helpcenteraction.ArticleSummary{{ID: qaEntryID, Title: "如何开发票？"}}, collections[0].Articles)
	require.Equal(t, documentBase.ID, collections[1].ID)
	require.Equal(t, []helpcenteraction.ArticleSummary{{ID: text.ID, Title: "退款说明"}}, collections[1].Articles)

	// 文章详情返回正文，文件文档与未发布知识库的文档不可读取。
	article, err := getArticle.Execute(ctx, channel.ID, text.ID)
	require.NoError(t, err)
	require.Equal(t, "# 退款\n\n签收后七天内可以申请退款。", article.Body)
	require.Equal(t, documentBase.Name, article.CollectionName)
	article, err = getArticle.Execute(ctx, channel.ID, qaEntryID)
	require.NoError(t, err)
	require.Equal(t, "如何开发票？", article.Title)
	require.Equal(t, "下单时选择电子发票。", article.Body)
	for _, id := range []string{fileDocumentID, internalDocumentID, "invalid"} {
		_, err := getArticle.Execute(ctx, channel.ID, id)
		require.ErrorIs(t, err, helpcenteraction.ErrArticleNotFound, "article %s", id)
	}

	// 搜索只召回已发布文章，按相关度排序。
	articles, err := search.Execute(ctx, channel.ID, "退款")
	require.NoError(t, err)
	require.NotEmpty(t, articles)
	require.Equal(t, text.ID, articles[0].ID)
	for _, article := range articles {
		require.NotEqual(t, fileDocumentID, article.ID, "unpublished article=%+v", article)
		require.NotEqual(t, internalDocumentID, article.ID, "unpublished article=%+v", article)
	}
	_, err = search.Execute(ctx, channel.ID, "  ")
	require.ErrorIs(t, err, helpcenteraction.ErrQueryInvalid)

	// 删除知识库后从帮助中心移除。
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, qaBase.ID))
	collections, err = getHelpCenter.Execute(ctx, channel.ID)
	require.NoError(t, err)
	require.Len(t, collections, 1)
	require.Equal(t, documentBase.ID, collections[0].ID)

	// 渠道停用后帮助中心不可访问。
	_, err = db.NewUpdate().Model((*servermodels.Channel)(nil)).Set("enabled = FALSE").Where("id = ?", channel.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = getHelpCenter.Execute(ctx, channel.ID)
	require.ErrorIs(t, err, helpcenteraction.ErrChannelNotFound)
	_, err = search.Execute(ctx, channel.ID, "退款")
	require.ErrorIs(t, err, helpcenteraction.ErrChannelNotFound)
}
