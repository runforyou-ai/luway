//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun/provider"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	servicecategoryaction "github.com/runforyou-ai/luway/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
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

// summaryCaller 是返回预设正文的对话模型上游，记录调用次数与最近一次用户输入。
type summaryCaller struct {
	text  string
	calls int
	input string
}

// chat 返回由 summaryCaller 回答的上游模型组件。
func (c *summaryCaller) chat(context.Context, provider.ChatConfig) (model.AgenticModel, error) {
	return c, nil
}

// Generate 记录用户输入并返回预设正文。
func (c *summaryCaller) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	c.calls++
	c.input = lastUserText(input)
	return assistantText(c.text), nil
}

// Stream 以单个分片返回 Generate 的结果。
func (c *summaryCaller) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := c.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// lastUserText 返回最后一条用户消息的文本。
func lastUserText(input []*schema.AgenticMessage) string {
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role != schema.AgenticRoleTypeUser {
			continue
		}
		var text strings.Builder
		for _, block := range input[i].ContentBlocks {
			if block.UserInputText != nil {
				text.WriteString(block.UserInputText.Text)
			}
		}
		return text.String()
	}
	return ""
}

// TestServiceSessionSummaryLifecycle 验证周期关闭后按设置生成小结与标注、客服修改后不再被 AI 覆盖，以及无实质诉求的周期只记标记。
func TestServiceSessionSummaryLifecycle(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	coordinator := newGroupAgentCoordinator(f.db)
	providerID := seedSummaryModels(t, f.db, f.owner)
	category, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.owner, servicecategoryaction.Input{Name: "订单查询", Description: "查询订单状态"})
	require.NoError(t, err)
	_, err = customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, f.db, providerID, "decision-model")),
		SummaryModelID:  new(aiModelID(t, f.db, providerID, "chat-model")),
		Locale:          domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)

	// 人工关闭后标记等待生成并投递小结任务。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	session := loadSummarySession(t, f.db, f.conversationID)
	require.Equal(t, new(string(domain.ServiceSessionSummaryPending)), session.SummaryStatus, "关闭后小结状态")
	input := loadSummarizeInput(t, session.ID, tasks, testEnqueuer)

	// 判断模型标注有实质诉求、已解决与咨询分类，小结模型生成正文。
	decider := &summaryDecider{answers: map[string]decision.Answer{
		"request":  {Kind: decision.KindYesNo, Probability: 0.95},
		"resolved": {Kind: decision.KindYesNo, Probability: 0.9},
		"category": {Kind: decision.KindChoice, Choice: category.ID, Probabilities: map[string]float64{category.ID: 0.8}},
	}}
	caller := &summaryCaller{text: "```json\n{\"summary\":\"客户询问订单状态，客服已告知预计送达时间。\"}\n```"}
	worker := servicesummary.NewWorker(f.db, tasks, modelcall.New(f.db, modelcall.Upstreams{Decider: decider, Chat: caller.chat}, nil))
	require.NoError(t, worker.Summarize(ctx, input))
	summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.Len(t, summaries.Sessions, 1)
	generated := summaries.Sessions[0]
	require.Equal(t, new(domain.ServiceSessionSummaryReady), generated.Status)
	require.Equal(t, new("客户询问订单状态，客服已告知预计送达时间。"), generated.Summary)
	require.Equal(t, new(true), generated.Resolved)
	require.Equal(t, &category.ID, generated.CategoryID)
	require.Equal(t, domain.ServiceSessionCloseManual, generated.CloseReason)
	require.Contains(t, decider.questions, "resolved", "人工关闭的周期应判断是否解决")

	// 分类归档后客服保留原分类修改小结；重开再关闭时小结保持客服填写的结果，不再投递任务。
	require.NoError(t, servicecategoryaction.NewArchiveAction(f.db).Execute(ctx, f.owner, category.ID))
	edited, err := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: session.ID, Summary: "  客服修改后的小结  ", Resolved: new(false), CategoryID: &category.ID,
	})
	require.NoError(t, err)
	require.Equal(t, new("客服修改后的小结"), edited.Summary)
	require.Equal(t, new(false), edited.Resolved)
	require.Equal(t, &category.ID, edited.CategoryID)
	require.NotNil(t, edited.EditedByName)
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	session = loadSummarySession(t, f.db, f.conversationID)
	require.Equal(t, new(string(domain.ServiceSessionSummaryReady)), session.SummaryStatus)
	require.Equal(t, new("客服修改后的小结"), session.Summary)
	require.NoError(t, worker.Summarize(ctx, input))
	session = loadSummarySession(t, f.db, f.conversationID)
	require.Equal(t, "客服修改后的小结", *session.Summary, "过期任务覆盖了客服修改的小结")

	// 客户新开周期只打招呼，判断为无实质诉求时不生成正文与标注。
	_, err = f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "你好",
	})
	require.NoError(t, err)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	greeting := loadSummarySession(t, f.db, f.conversationID)
	// 模拟转人工时写入的分类，无实质诉求时应清空。
	_, err = f.db.NewUpdate().Table("service_sessions").Set("category_id = ?", category.ID).Where("id = ?", greeting.ID).Exec(ctx)
	require.NoError(t, err)
	decider.answers["request"] = decision.Answer{Kind: decision.KindYesNo, Probability: 0.1}
	callsBefore := caller.calls
	require.NoError(t, worker.Summarize(ctx, loadSummarizeInput(t, greeting.ID, tasks, testEnqueuer)))
	greeting = loadSummarySession(t, f.db, f.conversationID)
	require.Equal(t, new(string(domain.ServiceSessionSummaryNoRequest)), greeting.SummaryStatus)
	require.Nil(t, greeting.Summary)
	require.Nil(t, greeting.Resolved)
	require.Nil(t, greeting.CategoryID)
	require.Equal(t, callsBefore, caller.calls, "模型调用次数")

	// 客服未填写任何内容保存时保持无实质诉求。
	kept, err := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{ServiceSessionID: greeting.ID})
	require.NoError(t, err)
	require.Equal(t, new(domain.ServiceSessionSummaryNoRequest), kept.Status, "未填写内容保存后的小结状态")

	// 历史小结只注入有正文的其他周期。
	history, err := servicesummary.RecentHistory(ctx, f.db, f.owner.Workspace.ID, greeting.ID, nil)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "客服修改后的小结", history[0].Summary)
	require.Equal(t, new(false), history[0].Resolved)
}

// TestHandoffSummary 验证转人工后生成交接摘要，只在周期开放时返回，更新的转人工使旧任务失效。
func TestHandoffSummary(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	providerID := seedSummaryModels(t, f.db, f.owner)
	_, err := customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		SummaryModelID: new(aiModelID(t, f.db, providerID, "chat-model")), Locale: domain.LocaleEnglishUnitedStates,
	})
	require.NoError(t, err)
	session := loadSummarySession(t, f.db, f.conversationID)
	// appendHandoff 写入一条转人工系统事件并准备交接摘要。
	appendHandoff := func() string {
		t.Helper()
		messageID := uuid.NewV7().String()
		payload, _ := json.Marshal(domain.ServiceSessionHandedOffEvent{ServiceSessionID: session.ID, Reason: domain.AgentHandoffReason("knowledge_gap")})
		eventType := string(domain.ConversationSystemEventServiceSessionHandedOff)
		require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := chatstate.LockServiceSession(ctx, tx, f.owner.Workspace.ID, f.conversationID)
			if err != nil {
				return err
			}
			if _, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, locked.Conversation, &servermodels.Message{
				ID: messageID, WorkspaceID: f.owner.Workspace.ID, ConversationID: f.conversationID, ServiceSessionID: &session.ID,
				Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
				SystemEventType: &eventType, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			return servicesummary.MarkHandedOff(ctx, tx, tasks, locked.Session, messageID)
		}))
		return messageID
	}
	first := appendHandoff()
	second := appendHandoff()
	// 转人工之后客户的新消息不进入交接摘要。
	_, err = f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "转人工之后的新消息",
	})
	require.NoError(t, err)
	caller := &summaryCaller{text: `{"request":"Where is my order?","progress":"AI could not find the order.","blocker":"Check the order system."}`}
	worker := servicesummary.NewWorker(f.db, tasks, modelcall.New(f.db, modelcall.Upstreams{Decider: &summaryDecider{}, Chat: caller.chat}, nil))
	// 旧的转人工任务不写入。
	require.NoError(t, worker.HandoffSummary(ctx, servicesummary.HandoffSummaryInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID, MessageID: first}))
	summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.Nil(t, summaries.Handoff, "旧转人工的交接摘要")
	require.NoError(t, worker.HandoffSummary(ctx, servicesummary.HandoffSummaryInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID, MessageID: second}))
	summaries, err = servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.NotNil(t, summaries.Handoff)
	require.Equal(t, "Where is my order?", summaries.Handoff.Request)
	require.Equal(t, "Check the order system.", summaries.Handoff.Blocker)
	require.Contains(t, caller.input, "客户首条消息")
	require.NotContains(t, caller.input, "转人工之后的新消息")

	// 周期关闭后不再返回交接摘要。
	coordinator := newGroupAgentCoordinator(f.db)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	summaries, err = servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.Nil(t, summaries.Handoff, "关闭后的交接摘要")
}

// seedSummaryModels 为企业添加一个含对话模型与判断模型的模型服务，返回供应商编号。
func seedSummaryModels(t *testing.T, db *bun.DB, identity *servermodels.Identity) string {
	t.Helper()
	ctx := context.Background()
	provider := &servermodels.AIProvider{
		WorkspaceID: &identity.Workspace.ID, Brand: string(domain.AIProviderBrandOpenAI), Name: "小结测试模型服务 " + uuid.NewV7().String(),
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "test-key", APIURL: "https://example.com/v1",
	}
	_, err := db.NewInsert().Model(provider).Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx)
	require.NoError(t, err)
	insertAIModels(t, db,
		&testAIModel{ProviderID: provider.ID, Identifier: "chat-model", Name: "对话模型", Type: string(domain.AIModelTypeChat),
			InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096},
		&testAIModel{ProviderID: provider.ID, Identifier: "decision-model", Name: "判断模型", Type: string(domain.AIModelTypeDecision),
			InputModalities: json.RawMessage(`["text"]`), ContextWindow: 32000},
	)
	return provider.ID
}

// loadSummarySession 读取客户会话的当前客服周期。
func loadSummarySession(t *testing.T, db *bun.DB, conversationID string) *servermodels.ServiceSession {
	t.Helper()
	session := &servermodels.ServiceSession{}
	require.NoError(t, db.NewSelect().Model(session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.workspace_id = ss.workspace_id").
		Where("svc.conversation_id = ?", conversationID).
		Scan(context.Background()))
	return session
}

// loadSummarizeInput 返回登记器中指定周期最近一次投递的小结任务输入。
func loadSummarizeInput(t *testing.T, serviceSessionID string, recorders ...*servertest.Tasks) servicesummary.SummarizeInput {
	t.Helper()
	return servertest.LatestInput(t, servicesummary.SummarizeActionName, func(input servicesummary.SummarizeInput) bool { return input.ServiceSessionID == serviceSessionID }, recorders...)
}
