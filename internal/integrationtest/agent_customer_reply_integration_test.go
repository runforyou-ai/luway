//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil)
			input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "最早的客户问题"}
			original, err := receive.Execute(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			input.ConversationID = &original.Conversation.ID
			for i := range 105 {
				input.ClientMessageID, input.Body = uuid.NewV7().String(), fmt.Sprintf("中间问题 %d", i)
				if _, err := receive.Execute(ctx, input); err != nil {
					t.Fatal(err)
				}
			}
			coordinator := agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil)
			if scenario.earlierSession {
				if _, err := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, original.Conversation.ID); err != nil {
					t.Fatal(err)
				}
				input.ClientMessageID, input.Body = uuid.NewV7().String(), "本轮客户问题"
				reopened, err := receive.Execute(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				if reopened.Conversation.ID != original.Conversation.ID || reopened.Conversation.ServiceSessionID == original.Conversation.ServiceSessionID {
					t.Fatalf("expected new session in same conversation: %+v", reopened.Conversation)
				}
			}
			if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{ConversationID: original.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "针对早期问题的回答", ReplyToMessageID: original.Message.ID}); err != nil {
				t.Fatal(err)
			}
			if deleted {
				if _, err := db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := servicesessionaction.NewTransferServiceSessionAction(db, coordinator, scheduler, newTestTasks(db)).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: original.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: created.IdentityID}); err != nil {
				t.Fatal(err)
			}
			input.ClientMessageID, input.Body = uuid.NewV7().String(), "请接着解释"
			if _, err := receive.Execute(ctx, input); err != nil {
				t.Fatal(err)
			}
			calls := 0
			runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				calls++
				if request.CustomerHistorySearch == nil {
					t.Fatal("customer history tool not provided")
				}
				if !slices.Contains(request.Assignment.Tools, agentruntime.CustomerHistoryToolName) {
					t.Fatalf("customer history tool not listed: %+v", request.Assignment.Tools)
				}
				// 只有已关闭的早期周期可被检索，当前周期不在结果中。
				history, err := request.CustomerHistorySearch(ctx, "中间问题")
				if err != nil {
					t.Fatal(err)
				}
				if scenario.earlierSession {
					if len(history.Sessions) != 1 || len(history.Sessions[0].Messages) == 0 {
						t.Fatalf("earlier session not found: %+v", history)
					}
					for _, message := range history.Sessions[0].Messages {
						if message.Sender != "customer" || !strings.HasPrefix(message.Body, "中间问题") {
							t.Fatalf("unexpected history message: %+v", message)
						}
					}
				} else if len(history.Sessions) != 0 || history.Message == "" {
					t.Fatalf("open session leaked into history: %+v", history)
				}
				triggers, err := feed.Peek(ctx, 0)
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
					if len(claimed.Messages) > 0 && claimed.Messages[0].Content != "本轮客户问题" {
						t.Fatalf("previous session leaked into current context: %+v", claimed.Messages)
					}
				}
				if len(claimed.Messages) != wantLength {
					t.Fatalf("history length=%d", len(claimed.Messages))
				}
				last, quoted := claimed.Messages[wantLength-1], claimed.Messages[wantLength-2]
				if last.Role != agentruntime.MessageRoleUser || last.Content != input.Body || quoted.Role != agentruntime.MessageRoleAssistant {
					t.Fatalf("roles/plain body changed: %+v %+v", last, quoted)
				}
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
				if err := json.Unmarshal([]byte(quoted.Content), &content); err != nil {
					t.Fatal(err)
				}
				reference := content.ReplyTo
				if content.Body != "针对早期问题的回答" || reference.MessageID != original.Message.ID || reference.Deleted != deleted {
					t.Fatalf("reference=%+v", content)
				}
				if deleted {
					if reference.Body != "" || reference.SenderSourceID != "" || reference.SenderKind != "" || reference.SenderName != "" || strings.Contains(quoted.Content, original.Message.Body) {
						t.Fatalf("deleted reference leaked: %s", quoted.Content)
					}
				} else if reference.Body != original.Message.Body || reference.SenderKind != string(domain.ChatSubjectKindContact) || reference.SenderSourceID == "" {
					t.Fatalf("customer reference=%+v", reference)
				}
				for _, message := range claimed.Messages[:wantLength-2] {
					if message.Content == original.Message.Body {
						t.Fatal("reference target should be outside history window")
					}
				}
				// 核验连续 Claim 按同一客服周期构造上下文。
				input.ClientMessageID, input.Body = uuid.NewV7().String(), "本轮补充信息"
				if _, err := receive.Execute(ctx, input); err != nil {
					return agentruntime.RunResult{}, err
				}
				pending, err := feed.Peek(ctx, claimed.EndSeq)
				if err != nil || len(pending) != 1 {
					t.Fatalf("pending triggers=%+v, error=%v", pending, err)
				}
				claimed, err = feed.Claim(ctx, pending[0].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				if len(claimed.Messages) != min(wantLength+1, 100) || claimed.Messages[len(claimed.Messages)-1].Content != input.Body {
					t.Fatalf("follow-up context=%+v", claimed.Messages)
				}
				return agentruntime.RunResult{Content: "AI 后续回答", EndSeq: claimed.EndSeq}, nil
			}}
			run := &servermodels.AgentRun{}
			if err := db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", original.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("runtime calls=%d", calls)
			}
			// 访客引用 AI 回复时，发送响应与历史页均保留 AI 身份。
			historyInput := customerchataction.MessageHistoryInput{ChannelID: channel.ID, ExternalID: input.ExternalID, ConversationID: original.Conversation.ID}
			history, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, historyInput)
			if err != nil || len(history.Messages) == 0 {
				t.Fatalf("AI history=%+v err=%v", history, err)
			}
			input.ClientMessageID, input.Body = uuid.NewV7().String(), "引用 AI 回答"
			input.ReplyToMessageID = history.Messages[len(history.Messages)-1].ID
			reply, err := receive.Execute(ctx, input)
			if err != nil || reply.Message.ReplyTo == nil || reply.Message.ReplyTo.SenderIdentityType == nil || *reply.Message.ReplyTo.SenderIdentityType != domain.OrganizationIdentityTypeAgent {
				t.Fatalf("AI reference response=%+v err=%v", reply, err)
			}
			history, err = customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, historyInput)
			if err != nil || len(history.Messages) == 0 {
				t.Fatalf("AI reference history=%+v err=%v", history, err)
			}
			if reference := history.Messages[len(history.Messages)-1].ReplyTo; reference == nil || reference.Body != "AI 后续回答" || reference.SenderIdentityType == nil || *reference.SenderIdentityType != domain.OrganizationIdentityTypeAgent {
				t.Fatalf("AI history reference=%+v", reference)
			}
		})
	}
	testCustomerFailureMessage(t, db, identity, tasks, created.IdentityID)
	testCustomerAgentLocking(t, db, identity, created.IdentityID, tasks)
	testCustomerAgentRunNotifications(t, db, identity, created.IdentityID, tasks)
}

// customerTurnMessages 核对客服运行上下文以系统提供的客户上下文消息开头，并返回其后的会话消息。
func customerTurnMessages(t *testing.T, messages []agentruntime.Message) []agentruntime.Message {
	t.Helper()
	if len(messages) == 0 || !strings.HasPrefix(messages[0].ID, "customer-context:") || !strings.Contains(messages[0].Content, `"kind":"customer_context"`) {
		t.Fatalf("客服上下文缺少客户上下文消息: %+v", messages)
	}
	return messages[1:]
}
