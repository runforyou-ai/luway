//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// testAgentFailureMessages 验证失败结果作为普通时间线记录持久化并排除于模型上下文。
func testAgentFailureMessages(t *testing.T, db *bun.DB, identity *servermodels.Identity, tasks *servertask.Runtime, conversationID, firstRunID, secondRunID, nextRunID string) {
	t.Helper()
	ctx := context.Background()
	query := conversationaction.NewListConversationMessagesQuery(db)
	failures := make(map[string]bool)
	processed := make(map[string]bool)
	runs := make(map[string]servermodels.AgentRun)
	finalizer := agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), nil, nil)
	for _, runID := range []string{firstRunID, secondRunID} {
		var run servermodels.AgentRun
		if err := db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if run.ResponseMessageID == nil {
			t.Fatal("failed run has no result message")
		}
		var message servermodels.Message
		if err := db.NewSelect().Model(&message).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if message.Type != string(domain.MessageTypeAgentError) || message.Body != "" || message.SenderParticipantID == nil || message.IdempotencyKey == nil || *message.IdempotencyKey != "agent:"+runID {
			t.Fatalf("failure message = %+v", message)
		}
		failures[message.ID] = true
		hasBlocks, err := db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", runID).Exists(ctx)
		if err != nil {
			t.Fatal(err)
		}
		processed[message.ID] = hasBlocks
		runs[message.ID] = run
		if err := finalizer.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: runID}, errors.New("duplicate completion")); err != nil {
			t.Fatal(err)
		}
	}
	history, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range history.Messages {
		if !failures[message.ID] {
			continue
		}
		count++
		// 中断前产生过内容的运行在失败消息上给出过程引用，没有产生内容的运行不给。
		if (message.AgentProcess != nil) != processed[message.ID] || message.Sender == nil || message.Sender.IdentityType == nil || *message.Sender.IdentityType != domain.OrganizationIdentityTypeAgent {
			t.Fatalf("failure sender = %+v, process expected = %v", message, processed[message.ID])
		}
		// 过程引用指向产生该失败消息的运行并携带其模型用量。
		if message.AgentProcess != nil {
			var usage agentruntime.Usage
			if err := json.Unmarshal(runs[message.ID].Usage, &usage); err != nil {
				t.Fatal(err)
			}
			if message.AgentProcess.ID != runs[message.ID].ID || usage.TotalTokens == 0 || message.AgentProcess.Usage != usage {
				t.Fatalf("failure process = %+v, run usage = %+v", message.AgentProcess, usage)
			}
		}
		cursor := &conversationaction.MessageCursorPoint{ID: message.ID, MessageSeq: message.MessageSeq}
		earlier, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Before: cursor})
		if err != nil || earlier.After == nil {
			t.Fatalf("error message pagination = %+v, %v", earlier, err)
		}
		later, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, After: earlier.After})
		if err != nil || len(later.Messages) == 0 || later.Messages[0].ID != message.ID {
			t.Fatalf("error message incremental page = %+v, %v", later, err)
		}
	}
	if count != 2 {
		t.Fatalf("failure message count = %d", count)
	}
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		pending, err := feed.Peek(ctx, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		for _, message := range claimed.Messages {
			if failures[message.ID] {
				t.Fatal("failure entered model context")
			}
		}
		return agentruntime.RunResult{EndSeq: claimed.EndSeq, Content: "恢复后的回复"}, nil
	}}
	if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: nextRunID}); err != nil {
		t.Fatal(err)
	}
	restored, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, message := range restored.Messages {
		if message.Type == domain.MessageTypeAgentError {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("next reply replaced error messages: %d", count)
	}
}

// testCustomerFailureMessage 验证开始执行前失败的客服运行转交人工：错误消息只在成员历史中可见，访客只看到对客通知，重复收尾各类消息只写一次。
func testCustomerFailureMessage(t *testing.T, db *bun.DB, identity *servermodels.Identity, tasks *servertask.Runtime, agentID string) {
	t.Helper()
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "首响失败验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	disableAutoAssignment(t, db, identity.Organization.ID)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), nil)
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "首条客服消息"}

	sent, err := receive.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var run servermodels.AgentRun
	if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", sent.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), nil, nil)
	for range 2 {
		if err := executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("model failure details")); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusFailed) || run.Outcome == nil || *run.Outcome != string(domain.AgentRunOutcomeHandoff) ||
		run.OutcomeReason == nil || *run.OutcomeReason != string(domain.AgentHandoffReasonRuntimeFailed) || run.HandoffSettledSeq == nil || *run.HandoffSettledSeq != 1 {
		t.Fatalf("failed run = %+v", run)
	}
	var session servermodels.ServiceSession
	if err := db.NewSelect().Model(&session).Where("ss.id = ?", run.ScopeID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if session.AssigneeIdentityID != nil || session.TeamID != nil || session.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("handed off session = %+v", session)
	}

	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: sent.Conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[domain.MessageType]int{}
	for _, message := range history.Messages {
		counts[message.Type]++
		switch message.Type {
		case domain.MessageTypeAgentError:
			if message.Visibility != domain.MessageVisibilityInternal {
				t.Fatalf("agent error visibility = %q", message.Visibility)
			}
		case domain.MessageTypeSystem:
			event := message.SystemEvent
			if event == nil || event.Type != domain.ConversationSystemEventServiceSessionHandedOff || event.Reason == nil ||
				*event.Reason != domain.AgentHandoffReasonRuntimeFailed || event.Target == nil || event.Target.Kind != domain.ServiceSessionTargetPublicQueue ||
				event.ReasonText == nil || *event.ReasonText != "model failure details" || event.AgentRunID == nil || *event.AgentRunID != run.ID {
				t.Fatalf("handoff event = %+v", event)
			}
		}
	}
	last := history.Messages[len(history.Messages)-1]
	if counts[domain.MessageTypeAgentError] != 1 || counts[domain.MessageTypeSystem] != 1 || counts[domain.MessageTypeText] != 2 ||
		last.ID != *run.ResponseMessageID || last.Type != domain.MessageTypeText || last.AgentProcess != nil {
		t.Fatalf("member history = %+v", history.Messages)
	}

	visibleDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(ctx, input.ChannelID, input.ExternalID)
	visible := visibleDirectory.Conversations
	if err != nil || len(visible) != 1 || visible[0].Preview != last.Body {
		t.Fatalf("visitor preview = %+v, %v", visible, err)
	}
	messages, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: input.ChannelID, ExternalID: input.ExternalID, ConversationID: sent.Conversation.ID})
	if err != nil || len(messages.Messages) != 2 || messages.Messages[1].ID != last.ID {
		t.Fatalf("visitor history = %+v, %v", messages, err)
	}
}
