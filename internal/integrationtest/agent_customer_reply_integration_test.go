//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestAgentCustomerReplies 验证客服上下文按周期隔离，并保留窗口外和跨周期的一层引用。
func TestAgentCustomerReplies(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "客服引用助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "结合引用回答"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	t.Run("客服共享主体竞争", func(t *testing.T) {
		testCustomerSharedAgentSubject(t, db, identity, created.IdentityID, tasks)
	})
	t.Run("访客输入回滚", func(t *testing.T) {
		testWebsiteAppendRollback(t, db, identity, created.IdentityID, tasks)
	})
	scheduler := agentrunaction.NewScheduler(tasks)
	for _, scenario := range []struct{ earlierSession, deleted bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("earlierSession=%t/deleted=%t", scenario.earlierSession, scenario.deleted), func(t *testing.T) {
			deleted := scenario.deleted
			channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
				Type: domain.ChannelTypeWebsite, Name: "引用上下文", DefaultLocale: domain.CustomerLocaleChineseSimplified,
				NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			})
			require.NoError(t, err)
			receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{})
			input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "最早的客户问题"}
			original, err := receive.Execute(ctx, input)
			require.NoError(t, err)
			input.ConversationID = &original.Conversation.ID
			for i := range 105 {
				input.ClientMessageID, input.Body = uuid.NewV7().String(), fmt.Sprintf("中间问题 %d", i)
				_, err := receive.Execute(ctx, input)
				require.NoError(t, err)
			}
			coordinator := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
			if scenario.earlierSession {
				_, err := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer).Execute(ctx, identity, original.Conversation.ID)
				require.NoError(t, err)
				input.ClientMessageID, input.Body = uuid.NewV7().String(), "本轮客户问题"
				reopened, err := receive.Execute(ctx, input)
				require.NoError(t, err)
				require.Equal(t, original.Conversation.ID, reopened.Conversation.ID, "expected new session in same conversation")
				require.NotEqual(t, original.Conversation.ServiceSessionID, reopened.Conversation.ServiceSessionID, "expected new session in same conversation")
			}
			_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{ConversationID: original.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "针对早期问题的回答", ReplyToMessageID: original.Message.ID})
			require.NoError(t, err)
			if deleted {
				_, err := db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx)
				require.NoError(t, err)
			}
			_, err = servicesessionaction.NewTransferServiceSessionAction(db, coordinator, scheduler, testEnqueuer).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: original.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: created.IdentityID})
			require.NoError(t, err)
			input.ClientMessageID, input.Body = uuid.NewV7().String(), "请接着解释"
			_, err = receive.Execute(ctx, input)
			require.NoError(t, err)
			calls := 0
			runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				calls++
				require.NotNil(t, request.CustomerHistorySearch, "customer history tool not provided")
				require.Contains(t, request.Assignment.Tools, agentruntime.CustomerHistoryToolName, "customer history tool not listed")
				// 只有已关闭的早期周期可被检索，当前周期不在结果中。
				history, err := request.CustomerHistorySearch(ctx, "中间问题")
				require.NoError(t, err)
				if scenario.earlierSession {
					require.Len(t, history.Sessions, 1, "earlier session not found")
					require.NotEmpty(t, history.Sessions[0].Messages, "earlier session not found")
					for _, message := range history.Sessions[0].Messages {
						require.Equal(t, "customer", message.Sender, "unexpected history message: %+v", message)
						require.True(t, strings.HasPrefix(message.Body, "中间问题"), "unexpected history message: %+v", message)
					}
				} else {
					require.Empty(t, history.Sessions, "open session leaked into history")
					require.NotEmpty(t, history.Message, "open session leaked into history")
				}
				triggers, err := pendingTriggers(ctx, feed, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				wantLength := 100
				if scenario.earlierSession {
					wantLength = 3
					if len(claimed.Messages) > 0 {
						require.Equal(t, "本轮客户问题", claimed.Messages[0].Content, "previous session leaked into current context")
					}
				}
				require.Len(t, claimed.Messages, wantLength)
				last, quoted := claimed.Messages[wantLength-1], claimed.Messages[wantLength-2]
				require.Equal(t, einorun.RoleUser, last.Role)
				require.Equal(t, input.Body, last.Content)
				require.Equal(t, einorun.RoleAssistant, quoted.Role)
				var content struct {
					Body    string `json:"body"`
					ReplyTo struct {
						MessageID      string `json:"messageId"`
						Deleted        bool   `json:"deleted"`
						SenderKind     string `json:"senderKind"`
						SenderSourceID string `json:"senderSourceId"`
						SenderName     string `json:"senderName"`
						Body           string `json:"body"`
					} `json:"replyTo"`
				}
				require.NoError(t, json.Unmarshal([]byte(quoted.Content), &content))
				reference := content.ReplyTo
				require.Equal(t, "针对早期问题的回答", content.Body)
				require.Equal(t, original.Message.ID, reference.MessageID)
				require.Equal(t, deleted, reference.Deleted)
				if deleted {
					require.Empty(t, reference.Body, "deleted reference leaked")
					require.Empty(t, reference.SenderSourceID, "deleted reference leaked")
					require.Empty(t, reference.SenderKind, "deleted reference leaked")
					require.Empty(t, reference.SenderName, "deleted reference leaked")
					require.NotContains(t, quoted.Content, original.Message.Body, "deleted reference leaked")
				} else {
					require.Equal(t, original.Message.Body, reference.Body)
					require.Equal(t, string(domain.ChatSubjectKindContact), reference.SenderKind)
					require.NotEmpty(t, reference.SenderSourceID)
				}
				for _, message := range claimed.Messages[:wantLength-2] {
					require.NotEqual(t, original.Message.Body, message.Content, "reference target should be outside history window")
				}
				// 核验连续 Claim 按同一客服周期构造上下文。
				input.ClientMessageID, input.Body = uuid.NewV7().String(), "本轮补充信息"
				if _, err := receive.Execute(ctx, input); err != nil {
					return agentruntime.RunResult{}, err
				}
				pending, err := pendingTriggers(ctx, feed, claimed.EndSeq)
				require.NoError(t, err)
				require.Len(t, pending, 1)
				claimed, err = feed.Claim(ctx, pending[0].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				require.Len(t, claimed.Messages, min(wantLength+1, 100), "follow-up context")
				require.Equal(t, input.Body, claimed.Messages[len(claimed.Messages)-1].Content, "follow-up context")
				return agentruntime.RunResult{Content: "AI 后续回答", EndSeq: claimed.EndSeq}, nil
			}}
			run := &servermodels.AgentRun{}
			require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", original.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx))
			require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			require.Equal(t, 1, calls, "runtime calls")
			// 访客引用 AI 回复时，发送响应与历史页均保留 AI 身份。
			historyInput := customerchataction.MessageHistoryInput{ChannelID: channel.ID, ExternalID: input.ExternalID, ConversationID: original.Conversation.ID}
			history, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, historyInput)
			require.NoError(t, err)
			require.NotEmpty(t, history.Messages, "AI history")
			input.ClientMessageID, input.Body = uuid.NewV7().String(), "引用 AI 回答"
			input.ReplyToMessageID = history.Messages[len(history.Messages)-1].ID
			reply, err := receive.Execute(ctx, input)
			require.NoError(t, err)
			require.NotNil(t, reply.Message.ReplyTo)
			require.NotNil(t, reply.Message.ReplyTo.SenderIdentityType)
			require.Equal(t, domain.WorkspaceIdentityTypeAgent, *reply.Message.ReplyTo.SenderIdentityType)
			history, err = customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, historyInput)
			require.NoError(t, err)
			require.NotEmpty(t, history.Messages, "AI reference history")
			reference := history.Messages[len(history.Messages)-1].ReplyTo
			require.NotNil(t, reference)
			require.Equal(t, "AI 后续回答", reference.Body)
			require.NotNil(t, reference.SenderIdentityType)
			require.Equal(t, domain.WorkspaceIdentityTypeAgent, *reference.SenderIdentityType)
		})
	}
	testCustomerFailureMessage(t, db, identity, tasks, created.IdentityID)
	testCustomerAgentLocking(t, db, identity, created.IdentityID, tasks)
	testCustomerAgentRunNotifications(t, db, identity, created.IdentityID, tasks)
}

// customerTurnMessages 核对客服运行上下文以系统提供的客户上下文消息开头，并返回其后的会话消息。
func customerTurnMessages(t *testing.T, messages []einorun.Message) []einorun.Message {
	t.Helper()
	require.NotEmpty(t, messages, "客服上下文缺少客户上下文消息")
	require.True(t, strings.HasPrefix(messages[0].ID, "customer-context:"), "客服上下文缺少客户上下文消息: %+v", messages)
	require.Contains(t, messages[0].Content, `"kind":"customer_context"`, "客服上下文缺少客户上下文消息")
	return messages[1:]
}
