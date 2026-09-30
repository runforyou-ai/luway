//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	servicecategoryaction "github.com/runforyou-ai/cervi/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/actions/servicesummary"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/decision"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// summaryDecider 按预设结果回答判断题，并记录收到的题目。
type summaryDecider struct {
	answers   map[string]decision.Answer
	questions map[string]decision.Question
}

// Decide 返回预设结果中与题目对应的回答。
func (d *summaryDecider) Decide(_ context.Context, _ decision.Credential, _ string, _ any, questions map[string]decision.Question) (map[string]decision.Answer, error) {
	d.questions = questions
	answers := make(map[string]decision.Answer, len(questions))
	for key := range questions {
		answers[key] = d.answers[key]
	}
	return answers, nil
}

// summaryCaller 返回预设的模型正文，并记录调用次数与最近一次输入。
type summaryCaller struct {
	text  string
	calls int
	input string
}

// CallOnce 返回预设的模型正文。
func (c *summaryCaller) CallOnce(_ context.Context, request agentruntime.SingleCallRequest) (agentruntime.SingleCallResult, error) {
	c.calls++
	c.input = request.Input
	return agentruntime.SingleCallResult{Text: c.text}, nil
}

// TestServiceSessionSummaryLifecycle 验证周期关闭后按设置生成小结与标注、客服修改后不再被 AI 覆盖，以及无实质诉求的周期只记标记。
func TestServiceSessionSummaryLifecycle(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := newTestTasks(f.db)
	coordinator := newGroupAgentCoordinator(f.db)
	providerID := seedSummaryModels(t, f.db, f.owner)
	category, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.owner, servicecategoryaction.Input{Name: "订单查询", Description: "查询订单状态"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "decision-model"},
		Summary:  &domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "chat-model"},
		Locale:   domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}

	// 人工关闭后标记等待生成并投递小结任务。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	session := loadSummarySession(t, f.db, f.conversationID)
	if session.SummaryStatus == nil || *session.SummaryStatus != string(domain.ServiceSessionSummaryPending) {
		t.Fatalf("关闭后小结状态 = %v", session.SummaryStatus)
	}
	input := loadSummarizeInput(t, f.db, session.ID)

	// 判断模型标注有实质诉求、已解决与咨询分类，小结模型生成正文。
	decider := &summaryDecider{answers: map[string]decision.Answer{
		"request":  {Kind: decision.KindYesNo, Probability: 0.95},
		"resolved": {Kind: decision.KindYesNo, Probability: 0.9},
		"category": {Kind: decision.KindChoice, Choice: category.ID, Probabilities: map[string]float64{category.ID: 0.8}},
	}}
	caller := &summaryCaller{text: "```json\n{\"summary\":\"客户询问订单状态，客服已告知预计送达时间。\"}\n```"}
	worker := servicesummary.NewWorker(f.db, tasks, decider, caller)
	if err := worker.Summarize(ctx, input); err != nil {
		t.Fatal(err)
	}
	summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries.Sessions) != 1 {
		t.Fatalf("周期小结 = %+v", summaries.Sessions)
	}
	generated := summaries.Sessions[0]
	if generated.Status == nil || *generated.Status != domain.ServiceSessionSummaryReady || generated.Summary == nil ||
		*generated.Summary != "客户询问订单状态，客服已告知预计送达时间。" || generated.Resolved == nil || !*generated.Resolved ||
		generated.CategoryID == nil || *generated.CategoryID != category.ID || generated.CloseReason != domain.ServiceSessionCloseManual {
		t.Fatalf("生成的小结 = %+v", generated)
	}
	if _, asked := decider.questions["resolved"]; !asked {
		t.Fatal("人工关闭的周期应判断是否解决")
	}

	// 分类归档后客服保留原分类修改小结；重开再关闭时小结保持客服填写的结果，不再投递任务。
	if err := servicecategoryaction.NewArchiveAction(f.db).Execute(ctx, f.owner, category.ID); err != nil {
		t.Fatal(err)
	}
	edited, err := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: session.ID, Summary: "  客服修改后的小结  ", Resolved: new(false), CategoryID: &category.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Summary == nil || *edited.Summary != "客服修改后的小结" || edited.Resolved == nil || *edited.Resolved ||
		edited.CategoryID == nil || *edited.CategoryID != category.ID || edited.EditedByName == nil {
		t.Fatalf("客服修改的小结 = %+v", edited)
	}
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	session = loadSummarySession(t, f.db, f.conversationID)
	if session.SummaryStatus == nil || *session.SummaryStatus != string(domain.ServiceSessionSummaryReady) ||
		session.Summary == nil || *session.Summary != "客服修改后的小结" {
		t.Fatalf("客服修改后再次关闭的小结 = %+v", session)
	}
	if err := worker.Summarize(ctx, input); err != nil {
		t.Fatal(err)
	}
	if session = loadSummarySession(t, f.db, f.conversationID); *session.Summary != "客服修改后的小结" {
		t.Fatalf("过期任务覆盖了客服修改的小结：%v", *session.Summary)
	}

	// 客户新开周期只打招呼，判断为无实质诉求时不生成正文与标注。
	if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "你好",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	greeting := loadSummarySession(t, f.db, f.conversationID)
	// 模拟转人工时写入的分类，无实质诉求时应清空。
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("category_id = ?", category.ID).Where("id = ?", greeting.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	decider.answers["request"] = decision.Answer{Kind: decision.KindYesNo, Probability: 0.1}
	callsBefore := caller.calls
	if err := worker.Summarize(ctx, loadSummarizeInput(t, f.db, greeting.ID)); err != nil {
		t.Fatal(err)
	}
	greeting = loadSummarySession(t, f.db, f.conversationID)
	if greeting.SummaryStatus == nil || *greeting.SummaryStatus != string(domain.ServiceSessionSummaryNoRequest) ||
		greeting.Summary != nil || greeting.Resolved != nil || greeting.CategoryID != nil || caller.calls != callsBefore {
		t.Fatalf("无实质诉求的周期 = %+v，模型调用 %d 次", greeting, caller.calls-callsBefore)
	}

	// 客服未填写任何内容保存时保持无实质诉求。
	kept, err := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{ServiceSessionID: greeting.ID})
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status == nil || *kept.Status != domain.ServiceSessionSummaryNoRequest {
		t.Fatalf("未填写内容保存后的小结状态 = %v", kept.Status)
	}

	// 历史小结只注入有正文的其他周期。
	history, err := servicesummary.RecentHistory(ctx, f.db, f.owner.Organization.ID, greeting.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Summary != "客服修改后的小结" || history[0].Resolved == nil || *history[0].Resolved {
		t.Fatalf("注入的历史小结 = %+v", history)
	}
}

// TestHandoffSummary 验证转人工后生成交接摘要，只在周期开放时返回，更新的转人工使旧任务失效。
func TestHandoffSummary(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := newTestTasks(f.db)
	providerID := seedSummaryModels(t, f.db, f.owner)
	if _, err := customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		Summary: &domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "chat-model"}, Locale: domain.LocaleEnglishUnitedStates,
	}); err != nil {
		t.Fatal(err)
	}
	session := loadSummarySession(t, f.db, f.conversationID)
	// appendHandoff 写入一条转人工系统事件并准备交接摘要。
	appendHandoff := func() string {
		t.Helper()
		messageID := uuid.NewV7().String()
		payload, _ := json.Marshal(domain.ServiceSessionHandedOffEvent{ServiceSessionID: session.ID, Reason: domain.AgentHandoffReason("knowledge_gap")})
		eventType := string(domain.ConversationSystemEventServiceSessionHandedOff)
		if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := chatstate.LockServiceSession(ctx, tx, f.owner.Organization.ID, f.conversationID)
			if err != nil {
				return err
			}
			if _, _, err := chatstate.AppendMessage(ctx, tx, locked.Conversation, &servermodels.Message{
				ID: messageID, OrganizationID: f.owner.Organization.ID, ConversationID: f.conversationID, ServiceSessionID: &session.ID,
				Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
				SystemEventType: &eventType, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			return servicesummary.MarkHandedOff(ctx, tx, tasks, locked.Session, messageID)
		}); err != nil {
			t.Fatal(err)
		}
		return messageID
	}
	first := appendHandoff()
	second := appendHandoff()
	// 转人工之后客户的新消息不进入交接摘要。
	if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "转人工之后的新消息",
	}); err != nil {
		t.Fatal(err)
	}
	caller := &summaryCaller{text: `{"request":"Where is my order?","progress":"AI could not find the order.","blocker":"Check the order system."}`}
	worker := servicesummary.NewWorker(f.db, tasks, &summaryDecider{}, caller)
	// 旧的转人工任务不写入。
	if err := worker.HandoffSummary(ctx, servicesummary.HandoffSummaryInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID, MessageID: first}); err != nil {
		t.Fatal(err)
	}
	if summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID); err != nil || summaries.Handoff != nil {
		t.Fatalf("旧转人工的交接摘要 = %+v, %v", summaries.Handoff, err)
	}
	if err := worker.HandoffSummary(ctx, servicesummary.HandoffSummaryInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID, MessageID: second}); err != nil {
		t.Fatal(err)
	}
	summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if summaries.Handoff == nil || summaries.Handoff.Request != "Where is my order?" || summaries.Handoff.Blocker != "Check the order system." {
		t.Fatalf("交接摘要 = %+v", summaries.Handoff)
	}
	if !strings.Contains(caller.input, "客户首条消息") || strings.Contains(caller.input, "转人工之后的新消息") {
		t.Fatalf("交接摘要资料 = %s", caller.input)
	}

	// 周期关闭后不再返回交接摘要。
	coordinator := newGroupAgentCoordinator(f.db)
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID); err != nil || summaries.Handoff != nil {
		t.Fatalf("关闭后的交接摘要 = %+v, %v", summaries.Handoff, err)
	}
}

// seedSummaryModels 为企业添加一个含对话模型与判断模型的模型服务，返回供应商编号。
func seedSummaryModels(t *testing.T, db *bun.DB, identity *servermodels.Identity) string {
	t.Helper()
	ctx := context.Background()
	provider := &servermodels.AIProvider{
		OrganizationID: identity.Organization.ID, Brand: string(domain.AIProviderBrandOpenAI), Name: "小结测试模型服务 " + uuid.NewV7().String(),
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "test-key", APIURL: "https://example.com/v1",
	}
	if _, err := db.NewInsert().Model(provider).Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	models := []servermodels.AIProviderModel{
		{ProviderID: provider.ID, OrganizationID: identity.Organization.ID, Identifier: "chat-model", Name: "对话模型", Type: string(domain.AIModelTypeChat),
			InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096},
		{ProviderID: provider.ID, OrganizationID: identity.Organization.ID, Identifier: "decision-model", Name: "判断模型", Type: string(domain.AIModelTypeDecision),
			InputModalities: json.RawMessage(`["text"]`), ContextWindow: 32000},
	}
	if _, err := db.NewInsert().Model(&models).Column("provider_id", "organization_id", "identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return provider.ID
}

// loadSummarySession 读取客户会话的当前客服周期。
func loadSummarySession(t *testing.T, db *bun.DB, conversationID string) *servermodels.ServiceSession {
	t.Helper()
	session := &servermodels.ServiceSession{}
	if err := db.NewSelect().Model(session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.organization_id = ss.organization_id").
		Where("svc.conversation_id = ?", conversationID).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

// loadSummarizeInput 读取指定周期最近一次投递的小结任务输入。
func loadSummarizeInput(t *testing.T, db *bun.DB, serviceSessionID string) servicesummary.SummarizeInput {
	t.Helper()
	var row struct {
		Payload json.RawMessage `bun:"payload"`
	}
	if err := db.NewSelect().TableExpr("task_runs AS tr").Column("tr.payload").
		Where("tr.action_name = ? AND tr.payload->>'serviceSessionId' = ?", servicesummary.SummarizeActionName, serviceSessionID).
		OrderExpr("tr.created_at DESC, tr.id DESC").Limit(1).
		Scan(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	var input servicesummary.SummarizeInput
	if err := json.Unmarshal(row.Payload, &input); err != nil {
		t.Fatal(err)
	}
	return input
}
