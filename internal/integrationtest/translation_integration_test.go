//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun/provider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// translationCaller 是按顺序返回预设正文的对话模型上游，记录调用次数。
type translationCaller struct {
	texts []string
	calls int
}

// chat 返回由 translationCaller 回答的上游模型组件。
func (c *translationCaller) chat(context.Context, provider.ChatConfig) (model.AgenticModel, error) {
	return c, nil
}

// Generate 返回下一条预设正文。
func (c *translationCaller) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	text := c.texts[c.calls]
	c.calls++
	return assistantText(text), nil
}

// Stream 以单个分片返回 Generate 的结果。
func (c *translationCaller) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := c.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// TestCustomerConversationTranslation 验证客户消息按需翻译并缓存、客户语言识别、翻译发送保存客服原文与幂等核对，以及回复语言锁定。
func TestCustomerConversationTranslation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	caller := &translationCaller{}
	translator := translationaction.NewTranslator(f.db, chatInvoker(f.db, caller.chat))
	visitorMessage, err := f.visitorMessage(ctx, "¿Dónde está mi pedido?")
	require.NoError(t, err)

	// 未设置翻译模型时翻译状态关闭，翻译请求报未开启。
	state, err := translator.ConversationState(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	require.False(t, state.Enabled)
	_, err = translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
	require.Same(t, translationaction.ErrDisabled, err)

	providerID := seedSummaryModels(t, f.db, f.owner)
	_, err = customerservice.NewUpdateTranslationSettingsAction(f.db).Execute(ctx, f.owner,
		new(aiModelID(t, f.db, providerID, "chat-model")))
	require.NoError(t, err)

	// 客户语言尚未识别时，回复按客户最近的消息推断语言后翻译。
	caller.texts = append(caller.texts, `{"language":"es","translation":"Hola"}`)
	inferred, err := translator.TranslateReply(ctx, f.owner, f.conversationID, "你好")
	require.NoError(t, err)
	require.NotNil(t, inferred)
	require.Equal(t, "es", inferred.Language)
	require.Equal(t, "Hola", inferred.Body)

	// 模型漏返回的消息以空译文返回，调用方按翻译失败处理，不保存语言。
	caller.texts = append(caller.texts, `{"items":[]}`)
	missing, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
	require.NoError(t, err)
	require.Len(t, missing, 1)
	require.Empty(t, missing[0].Language)
	require.Empty(t, missing[0].Body)

	// 模型对外语消息只返回空白译文时按翻译失败处理，不保存译文，下次请求重新翻译。
	caller.texts = append(caller.texts, `{"items":[{"i":"0","language":"es","translation":" "}]}`)
	blank, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
	require.NoError(t, err)
	require.Len(t, blank, 1)
	require.Equal(t, "es", blank[0].Language)
	require.Empty(t, blank[0].Body)

	// 首次翻译识别语言并保存译文，再次读取直接使用已保存的译文。
	calls := caller.calls
	caller.texts = append(caller.texts, `{"items":[{"i":"0","language":"es","translation":"我的订单在哪里？"}]}`)
	for range 2 {
		results, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, "es", results[0].Language)
		require.Equal(t, "我的订单在哪里？", results[0].Body)
	}
	require.Equal(t, calls+1, caller.calls, "model calls")
	state, err = translator.ConversationState(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	require.Equal(t, translationaction.ConversationState{
		Enabled: true, ViewerLanguage: f.owner.Account.Locale, CustomerLanguage: "es", ReplyLanguageLocked: false,
	}, state)

	// 翻译发送时客户收到译文，客服原话按客服语言保存，时间线向客服展示原话。
	caller.texts = append(caller.texts, `{"language":"es","translation":"Su pedido ya fue enviado."}`)
	translated, err := translator.TranslateReply(ctx, f.owner, f.conversationID, "您的订单已经发出。")
	require.NoError(t, err)
	require.NotNil(t, translated)
	require.Equal(t, "es", translated.Language)
	require.Equal(t, f.owner.Account.Locale, translated.SourceLanguage)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	input := servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "您的订单已经发出。",
		Translation: &servicesessionaction.OutgoingTranslation{Language: translated.Language, SourceLanguage: translated.SourceLanguage, Body: translated.Body},
	}
	sent, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, "Su pedido ya fue enviado.", sent.Body)
	require.NotNil(t, sent.Language)
	require.Equal(t, "es", *sent.Language)
	require.NotNil(t, sent.Translation)
	require.Equal(t, "您的订单已经发出。", sent.Translation.Body)
	// 同一发送重试时译文可能不同，按客服原话核对幂等。
	input.Translation = &servicesessionaction.OutgoingTranslation{Language: "es", SourceLanguage: translated.SourceLanguage, Body: "Su pedido se envió ayer."}
	replayed, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, sent.ID, replayed.ID)
	require.Equal(t, sent.Body, replayed.Body)
	// 重试发送沿用已发出的译文，不再调用模型。
	saved, err := send.SavedTranslation(ctx, f.owner, input.ClientMessageID)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, sent.Body, saved.Body)
	require.Equal(t, "es", saved.Language)
	require.Equal(t, translated.SourceLanguage, saved.SourceLanguage)
	// 预览译文的语言须仍是当前回复语言。
	require.NoError(t, translator.ValidateReplyLanguage(ctx, f.owner, f.conversationID, "es-MX"), "validate same reply language")
	require.Same(t, translationaction.ErrReplyLanguageChanged, translator.ValidateReplyLanguage(ctx, f.owner, f.conversationID, "en"), "validate changed reply language")
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	found := arr.KeyBy(history.Messages, func(message conversationaction.ConversationMessage) string { return message.ID })
	require.NotNil(t, found[sent.ID].Translation, "timeline sent message")
	require.Equal(t, "您的订单已经发出。", found[sent.ID].Translation.Body, "timeline sent message")
	require.NotNil(t, found[visitorMessage.Message.ID].Translation, "timeline visitor message")
	require.Equal(t, "我的订单在哪里？", found[visitorMessage.Message.ID].Translation.Body, "timeline visitor message")
	var authored []servermodels.MessageTranslation
	require.NoError(t, f.db.NewSelect().Model(&authored).Where("message_id = ?", sent.ID).Scan(ctx))
	require.Len(t, authored, 1)

	// 预览发送成功后回复语言改变，同一发送编号的重试仍返回已保存的消息。
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.ownerEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, translator, nil)
	requestCtx, meta := ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}
	previewInput := appservice.ServiceTextMessageInput{
		ClientMessageID: uuid.NewV7().String(), Body: "马上为您查询。",
		Translation: &appservice.CustomerReplyTranslation{Language: "es", Body: "Lo consulto enseguida."},
	}
	previewed, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	require.NoError(t, err)
	require.Equal(t, "Lo consulto enseguida.", previewed.Body)
	_, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "fr")
	require.NoError(t, err)
	retried, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	require.NoError(t, err)
	require.Equal(t, previewed.ID, retried.ID)
	// 改变本人语言设置后，同一发送编号的重试仍按首次保存的原话核对，自动翻译的重试不再调用模型。
	_, err = useraction.NewUpdatePreferencesAction(f.db).Execute(ctx, f.owner, useraction.PreferencesInput{
		Locale: domain.LocaleChineseSimplified, TranslationLanguage: "fr", TimeZone: "UTC",
	})
	require.NoError(t, err)
	relogin := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.ownerEmail, "password123")
	require.NotNil(t, relogin.Identity.User.TranslationLanguage)
	require.Equal(t, "fr", *relogin.Identity.User.TranslationLanguage)
	retried, err = backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	require.NoError(t, err, "preview retry after locale change")
	require.Equal(t, previewed.ID, retried.ID, "preview retry after locale change")
	calls = caller.calls
	autoInput := appservice.ServiceTextMessageInput{ClientMessageID: input.ClientMessageID, Body: input.Body, Translate: true}
	retried, err = backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, autoInput)
	require.NoError(t, err, "auto retry after locale change")
	require.Equal(t, sent.ID, retried.ID, "auto retry after locale change")
	require.Equal(t, calls, caller.calls, "auto retry after locale change")

	// 回复语言已变化时，新的预览译文被拒绝。
	previewInput.ClientMessageID = uuid.NewV7().String()
	_, err = backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	require.Error(t, err, "stale preview accepted")

	// 繁体中文客户的系统话术使用中文。
	_, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "zh-TW")
	require.NoError(t, err)
	locale, err := translationaction.CustomerLocale(ctx, f.db, f.owner.Workspace.ID, f.conversationID, domain.CustomerLocaleEnglishUnitedStates)
	require.NoError(t, err)
	require.Equal(t, domain.CustomerLocaleChineseSimplified, locale, "traditional chinese locale")

	// 锁定回复语言后按锁定语言回复，转人工话术选用对应的对客语言；解除后恢复识别结果。
	state, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "en")
	require.NoError(t, err)
	require.Equal(t, "en", state.CustomerLanguage)
	require.True(t, state.ReplyLanguageLocked)
	locale, err = translationaction.CustomerLocale(ctx, f.db, f.owner.Workspace.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	require.NoError(t, err)
	require.Equal(t, domain.CustomerLocaleEnglishUnitedStates, locale)
	_, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "hi")
	require.NoError(t, err)
	locale, err = translationaction.CustomerLocale(ctx, f.db, f.owner.Workspace.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	require.NoError(t, err)
	require.Equal(t, domain.CustomerLocaleHindiIndia, locale)
	state, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "")
	require.NoError(t, err)
	require.Equal(t, "es", state.CustomerLanguage)
	require.False(t, state.ReplyLanguageLocked)
	locale, err = translationaction.CustomerLocale(ctx, f.db, f.owner.Workspace.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	require.NoError(t, err)
	require.Equal(t, domain.CustomerLocaleChineseSimplified, locale, "fallback customer locale")
}
