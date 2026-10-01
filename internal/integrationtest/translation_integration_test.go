//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// translationCaller 按顺序返回预设的模型正文，并记录调用次数。
type translationCaller struct {
	texts []string
	calls int
}

// CallOnce 返回下一条预设正文。
func (c *translationCaller) CallOnce(_ context.Context, _ agentruntime.SingleCallRequest) (agentruntime.SingleCallResult, error) {
	text := c.texts[c.calls]
	c.calls++
	return agentruntime.SingleCallResult{Text: text}, nil
}

// TestCustomerConversationTranslation 验证客户消息按需翻译并缓存、客户语言识别、翻译发送保存客服原文与幂等核对，以及回复语言锁定。
func TestCustomerConversationTranslation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	caller := &translationCaller{}
	translator := translationaction.NewTranslator(f.db, caller)
	visitorMessage, err := f.visitorMessage(ctx, "¿Dónde está mi pedido?")
	if err != nil {
		t.Fatal(err)
	}

	// 未设置翻译模型时翻译状态关闭，翻译请求报未开启。
	state, err := translator.ConversationState(ctx, f.owner, f.conversationID)
	if err != nil || state.Enabled {
		t.Fatalf("state without model = %+v err=%v", state, err)
	}
	if _, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID}); err != translationaction.ErrDisabled {
		t.Fatalf("translate without model err = %v", err)
	}

	providerID := seedSummaryModels(t, f.db, f.owner)
	if _, err := customerservice.NewUpdateTranslationSettingsAction(f.db).Execute(ctx, f.owner,
		&domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "chat-model"}); err != nil {
		t.Fatal(err)
	}

	// 客户语言尚未识别时，回复按客户最近的消息推断语言后翻译。
	caller.texts = append(caller.texts, `{"language":"es","translation":"Hola"}`)
	inferred, err := translator.TranslateReply(ctx, f.owner, f.conversationID, "你好")
	if err != nil || inferred == nil || inferred.Language != "es" || inferred.Body != "Hola" {
		t.Fatalf("inferred reply = %+v err=%v", inferred, err)
	}

	// 模型漏返回的消息以空译文返回，调用方按翻译失败处理，不保存语言。
	caller.texts = append(caller.texts, `{"items":[]}`)
	missing, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
	if err != nil || len(missing) != 1 || missing[0].Language != "" || missing[0].Body != "" {
		t.Fatalf("missing results = %+v err=%v", missing, err)
	}

	// 首次翻译识别语言并保存译文，再次读取直接使用已保存的译文。
	calls := caller.calls
	caller.texts = append(caller.texts, `{"items":[{"i":"0","language":"es","translation":"我的订单在哪里？"}]}`)
	for range 2 {
		results, err := translator.TranslateMessages(ctx, f.owner, f.conversationID, []string{visitorMessage.Message.ID})
		if err != nil || len(results) != 1 || results[0].Language != "es" || results[0].Body != "我的订单在哪里？" {
			t.Fatalf("translate results = %+v err=%v", results, err)
		}
	}
	if caller.calls != calls+1 {
		t.Fatalf("model calls = %d, want %d", caller.calls, calls+1)
	}
	state, err = translator.ConversationState(ctx, f.owner, f.conversationID)
	if err != nil || !state.Enabled || state.CustomerLanguage != "es" || state.ReplyLanguageLocked || state.ViewerLanguage != f.owner.Account.Locale {
		t.Fatalf("state = %+v err=%v", state, err)
	}

	// 翻译发送时客户收到译文，客服原话按客服语言保存，时间线向客服展示原话。
	caller.texts = append(caller.texts, `{"language":"es","translation":"Su pedido ya fue enviado."}`)
	translated, err := translator.TranslateReply(ctx, f.owner, f.conversationID, "您的订单已经发出。")
	if err != nil || translated == nil || translated.Language != "es" || translated.SourceLanguage != f.owner.Account.Locale {
		t.Fatalf("translated reply = %+v err=%v", translated, err)
	}
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	input := servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "您的订单已经发出。",
		Translation: &servicesessionaction.OutgoingTranslation{Language: translated.Language, SourceLanguage: translated.SourceLanguage, Body: translated.Body},
	}
	sent, err := send.Execute(ctx, f.owner, input)
	if err != nil || sent.Body != "Su pedido ya fue enviado." || sent.Language == nil || *sent.Language != "es" ||
		sent.Translation == nil || sent.Translation.Body != "您的订单已经发出。" {
		t.Fatalf("sent = %+v err=%v", sent, err)
	}
	// 同一发送重试时译文可能不同，按客服原话核对幂等。
	input.Translation = &servicesessionaction.OutgoingTranslation{Language: "es", SourceLanguage: translated.SourceLanguage, Body: "Su pedido se envió ayer."}
	replayed, err := send.Execute(ctx, f.owner, input)
	if err != nil || replayed.ID != sent.ID || replayed.Body != sent.Body {
		t.Fatalf("replayed = %+v err=%v", replayed, err)
	}
	// 重试发送沿用已发出的译文，不再调用模型。
	saved, err := send.SavedTranslation(ctx, f.owner, input.ClientMessageID)
	if err != nil || saved == nil || saved.Body != sent.Body || saved.Language != "es" || saved.SourceLanguage != translated.SourceLanguage {
		t.Fatalf("saved translation = %+v err=%v", saved, err)
	}
	// 预览译文的语言须仍是当前回复语言。
	if err := translator.ValidateReplyLanguage(ctx, f.owner, f.conversationID, "es-MX"); err != nil {
		t.Fatalf("validate same reply language err = %v", err)
	}
	if err := translator.ValidateReplyLanguage(ctx, f.owner, f.conversationID, "en"); err != translationaction.ErrReplyLanguageChanged {
		t.Fatalf("validate changed reply language err = %v", err)
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]conversationaction.ConversationMessage{}
	for _, message := range history.Messages {
		found[message.ID] = message
	}
	if message := found[sent.ID]; message.Translation == nil || message.Translation.Body != "您的订单已经发出。" {
		t.Fatalf("timeline sent message = %+v", message)
	}
	if message := found[visitorMessage.Message.ID]; message.Translation == nil || message.Translation.Body != "我的订单在哪里？" {
		t.Fatalf("timeline visitor message = %+v", message)
	}
	var authored []servermodels.MessageTranslation
	if err := f.db.NewSelect().Model(&authored).Where("message_id = ?", sent.ID).Scan(ctx); err != nil || len(authored) != 1 {
		t.Fatalf("authored translations = %+v err=%v", authored, err)
	}

	// 预览发送成功后回复语言改变，同一发送编号的重试仍返回已保存的消息。
	login := loginMember(t, f.db, f.owner.Organization.ID, f.ownerEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, translator)
	requestCtx, meta := ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}
	previewInput := appservice.ServiceTextMessageInput{
		ClientMessageID: uuid.NewV7().String(), Body: "马上为您查询。",
		Translation: &appservice.CustomerReplyTranslation{Language: "es", Body: "Lo consulto enseguida."},
	}
	previewed, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	if err != nil || previewed.Body != "Lo consulto enseguida." {
		t.Fatalf("previewed send = %+v err=%v", previewed, err)
	}
	if _, err := translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "fr"); err != nil {
		t.Fatal(err)
	}
	retried, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput)
	if err != nil || retried.ID != previewed.ID {
		t.Fatalf("retried send = %+v err=%v", retried, err)
	}
	// 改变本人语言设置后，同一发送编号的重试仍按首次保存的原话核对，自动翻译的重试不再调用模型。
	if _, err := useraction.NewUpdatePreferencesAction(f.db).Execute(ctx, f.owner, useraction.PreferencesInput{
		Locale: domain.LocaleChineseSimplified, TranslationLanguage: "fr", TimeZone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	relogin := loginMember(t, f.db, f.owner.Organization.ID, f.ownerEmail, "password123")
	if relogin.Identity.User.TranslationLanguage == nil || *relogin.Identity.User.TranslationLanguage != "fr" {
		t.Fatalf("translation language after login = %+v", relogin.Identity.User.TranslationLanguage)
	}
	if retried, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput); err != nil || retried.ID != previewed.ID {
		t.Fatalf("preview retry after locale change = %+v err=%v", retried, err)
	}
	calls = caller.calls
	autoInput := appservice.ServiceTextMessageInput{ClientMessageID: input.ClientMessageID, Body: input.Body, Translate: true}
	if retried, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, autoInput); err != nil || retried.ID != sent.ID || caller.calls != calls {
		t.Fatalf("auto retry after locale change = %+v err=%v calls=%d", retried, err, caller.calls-calls)
	}

	// 回复语言已变化时，新的预览译文被拒绝。
	previewInput.ClientMessageID = uuid.NewV7().String()
	if _, err := backend.SendServiceTextMessage(requestCtx, meta, f.conversationID, previewInput); err == nil {
		t.Fatal("stale preview accepted")
	}

	// 繁体中文客户的系统话术使用中文。
	if _, err := translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "zh-TW"); err != nil {
		t.Fatal(err)
	}
	if locale, err := translationaction.CustomerLocale(ctx, f.db, f.owner.Organization.ID, f.conversationID, domain.CustomerLocaleEnglishUnitedStates); err != nil || locale != domain.CustomerLocaleChineseSimplified {
		t.Fatalf("traditional chinese locale = %s err=%v", locale, err)
	}

	// 锁定回复语言后按锁定语言回复，转人工话术选用对应的对客语言；解除后恢复识别结果。
	state, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "en")
	if err != nil || state.CustomerLanguage != "en" || !state.ReplyLanguageLocked {
		t.Fatalf("locked state = %+v err=%v", state, err)
	}
	locale, err := translationaction.CustomerLocale(ctx, f.db, f.owner.Organization.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	if err != nil || locale != domain.CustomerLocaleEnglishUnitedStates {
		t.Fatalf("customer locale = %s err=%v", locale, err)
	}
	if _, err := translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "hi"); err != nil {
		t.Fatal(err)
	}
	locale, err = translationaction.CustomerLocale(ctx, f.db, f.owner.Organization.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	if err != nil || locale != domain.CustomerLocaleHindiIndia {
		t.Fatalf("hindi customer locale = %s err=%v", locale, err)
	}
	state, err = translator.SetReplyLanguage(ctx, f.owner, f.conversationID, "")
	if err != nil || state.CustomerLanguage != "es" || state.ReplyLanguageLocked {
		t.Fatalf("unlocked state = %+v err=%v", state, err)
	}
	locale, err = translationaction.CustomerLocale(ctx, f.db, f.owner.Organization.ID, f.conversationID, domain.CustomerLocaleChineseSimplified)
	if err != nil || locale != domain.CustomerLocaleChineseSimplified {
		t.Fatalf("fallback customer locale = %s err=%v", locale, err)
	}
}
