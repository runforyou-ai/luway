//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"

	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	helpcenteraction "github.com/runforyou-ai/cervi/internal/actions/helpcenter"
	knowledgeaction "github.com/runforyou-ai/cervi/internal/actions/knowledgebase"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servertest "github.com/runforyou-ai/cervi/internal/servertest"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestWebsiteHelpCenter 验证网站渠道帮助中心的发布范围、合集与文章读取、只检索已发布文章、渠道停用与知识库删除。
func TestWebsiteHelpCenter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed, documentBase := newDocumentFixture(t, db)
	identity := installed.Identity
	qaBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "常见问题", domain.KnowledgeBaseCategoryQA))
	if err != nil {
		t.Fatal(err)
	}
	internalBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "内部资料", domain.KnowledgeBaseCategoryStandard))
	if err != nil {
		t.Fatal(err)
	}
	probe := &retrievalProbe{}
	text, err := knowledgeaction.NewSaveTextDocumentAction(db, newKnowledgeTasks(t, db)).Execute(ctx, identity, documentBase.ID, "", knowledgeaction.TextDocumentInput{Title: "退款说明", Content: "# 退款\n\n签收后七天内可以申请退款。"})
	if err != nil {
		t.Fatal(err)
	}
	runDocumentProcessing(t, db, probe, identity.Organization.ID, documentBase.ID, text.ID, false)
	fileDocumentID := publishRetrievalDocument(t, db, probe, identity, documentBase, "退款内部流程.txt", "退款审批由财务复核。")
	internalDocumentID := publishRetrievalDocument(t, db, probe, identity, internalBase, "退款底线.txt", "退款最多补偿五十元。")
	qaEntryID := publishQAEntry(t, db, probe, identity, qaBase, knowledgeaction.QAInput{Question: "如何开发票？", Answer: "下单时选择电子发票。"})

	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "帮助中心验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	getHelpCenter := helpcenteraction.NewGetHelpCenterQuery(db)
	getArticle := helpcenteraction.NewGetArticleQuery(db)
	search := helpcenteraction.NewSearchQuery(db, knowledgeaction.NewRetrievalService(db, probe, probe))

	// 未发布任何知识库时没有合集，搜索不返回结果。
	if collections, err := getHelpCenter.Execute(ctx, channel.ID); err != nil || len(collections) != 0 {
		t.Fatalf("collections=%+v err=%v", collections, err)
	}

	// 发布范围去重后按知识库名称排序，不属于本企业或格式错误的编号被拒绝。
	update := channelaction.NewUpdateWebsiteChannelHelpCenterAction(db)
	if _, err := update.Execute(ctx, identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{KnowledgeBaseIDs: []string{"invalid"}}); !errors.As(err, new(*common.FieldError)) {
		t.Fatalf("invalid err=%v", err)
	}
	other, _ := newDocumentFixture(t, db)
	if _, err := update.Execute(ctx, other.Identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{}); !errors.Is(err, channelaction.ErrNotFound) {
		t.Fatalf("other organization err=%v", err)
	}
	published, err := update.Execute(ctx, identity, channel.ID, channelaction.WebsiteChannelHelpCenterInput{Enabled: false, KnowledgeBaseIDs: []string{qaBase.ID, documentBase.ID, qaBase.ID}})
	if err != nil || published.Enabled || !slices.Equal(published.KnowledgeBaseIDs, []string{qaBase.ID, documentBase.ID}) {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	detail, err := channelaction.NewGetWebsiteChannelQuery(db).Execute(ctx, identity, channel.ID)
	if err != nil || detail.ChatInterface.HelpEnabled || !slices.Equal(detail.HelpCenterKnowledgeBaseIDs, published.KnowledgeBaseIDs) {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}

	// 合集只包含在线编写的文档与问答条目。
	collections, err := getHelpCenter.Execute(ctx, channel.ID)
	if err != nil || len(collections) != 2 {
		t.Fatalf("collections=%+v err=%v", collections, err)
	}
	if collections[0].ID != qaBase.ID || len(collections[0].Articles) != 1 || collections[0].Articles[0] != (helpcenteraction.ArticleSummary{ID: qaEntryID, Title: "如何开发票？"}) {
		t.Fatalf("qa collection=%+v", collections[0])
	}
	if collections[1].ID != documentBase.ID || len(collections[1].Articles) != 1 || collections[1].Articles[0] != (helpcenteraction.ArticleSummary{ID: text.ID, Title: "退款说明"}) {
		t.Fatalf("document collection=%+v", collections[1])
	}

	// 文章详情返回正文，文件文档与未发布知识库的文档不可读取。
	article, err := getArticle.Execute(ctx, channel.ID, text.ID)
	if err != nil || article.Body != "# 退款\n\n签收后七天内可以申请退款。" || article.CollectionName != documentBase.Name {
		t.Fatalf("article=%+v err=%v", article, err)
	}
	article, err = getArticle.Execute(ctx, channel.ID, qaEntryID)
	if err != nil || article.Title != "如何开发票？" || article.Body != "下单时选择电子发票。" {
		t.Fatalf("qa article=%+v err=%v", article, err)
	}
	for _, id := range []string{fileDocumentID, internalDocumentID, "invalid"} {
		if _, err := getArticle.Execute(ctx, channel.ID, id); !errors.Is(err, helpcenteraction.ErrArticleNotFound) {
			t.Fatalf("article %s err=%v", id, err)
		}
	}

	// 搜索只召回已发布文章，按相关度排序。
	articles, err := search.Execute(ctx, channel.ID, "退款")
	if err != nil || len(articles) == 0 || articles[0].ID != text.ID {
		t.Fatalf("articles=%+v err=%v", articles, err)
	}
	for _, article := range articles {
		if article.ID == fileDocumentID || article.ID == internalDocumentID {
			t.Fatalf("unpublished article=%+v", article)
		}
	}
	if _, err := search.Execute(ctx, channel.ID, "  "); !errors.Is(err, helpcenteraction.ErrQueryInvalid) {
		t.Fatalf("empty query err=%v", err)
	}

	// 删除知识库后从帮助中心移除。
	if err := knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, qaBase.ID); err != nil {
		t.Fatal(err)
	}
	collections, err = getHelpCenter.Execute(ctx, channel.ID)
	if err != nil || len(collections) != 1 || collections[0].ID != documentBase.ID {
		t.Fatalf("after delete collections=%+v err=%v", collections, err)
	}

	// 渠道停用后帮助中心不可访问。
	if _, err := db.NewUpdate().Model((*servermodels.Channel)(nil)).Set("enabled = FALSE").Where("id = ?", channel.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := getHelpCenter.Execute(ctx, channel.ID); !errors.Is(err, helpcenteraction.ErrChannelNotFound) {
		t.Fatalf("disabled err=%v", err)
	}
	if _, err := search.Execute(ctx, channel.ID, "退款"); !errors.Is(err, helpcenteraction.ErrChannelNotFound) {
		t.Fatalf("disabled search err=%v", err)
	}
}
