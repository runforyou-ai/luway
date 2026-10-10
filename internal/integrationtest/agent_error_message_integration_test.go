//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testAgentFailureMessages 验证失败结果作为普通时间线记录持久化并排除于模型上下文。
func testAgentFailureMessages(t *testing.T, db *bun.DB, identity *servermodels.Identity, tasks *servertest.Tasks, conversationID, firstRunID, secondRunID, nextRunID string) {
	t.Helper()
	ctx := context.Background()
	query := conversationaction.NewListConversationMessagesQuery(db)
	failures := make(map[string]bool)
	processed := make(map[string]bool)
	runs := make(map[string]servermodels.AgentRun)
	finalizer := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	for _, runID := range []string{firstRunID, secondRunID} {
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx))
		require.NotNil(t, run.ResponseMessageID, "failed run has no result message")
		var message servermodels.Message
		require.NoError(t, db.NewSelect().Model(&message).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx))
		require.Equal(t, string(domain.MessageTypeAgentError), message.Type)
		require.Empty(t, message.Body)
		require.NotNil(t, message.SenderParticipantID)
		require.NotNil(t, message.IdempotencyKey)
		require.Equal(t, "agent:"+runID, *message.IdempotencyKey)
		failures[message.ID] = true
		hasBlocks, err := db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", runID).Exists(ctx)
		require.NoError(t, err)
		processed[message.ID] = hasBlocks
		runs[message.ID] = run
		require.NoError(t, finalizer.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: runID}, errors.New("duplicate completion")))
	}
	history, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	count := 0
	for _, message := range history.Messages {
		if !failures[message.ID] {
			continue
		}
		count++
		// 中断前产生过内容的运行在失败消息上给出过程引用，没有产生内容的运行不给。
		require.Equal(t, processed[message.ID], message.AgentProcess != nil, "failure sender = %+v", message)
		require.NotNil(t, message.Sender)
		require.NotNil(t, message.Sender.IdentityType)
		require.Equal(t, domain.WorkspaceIdentityTypeAgent, *message.Sender.IdentityType)
		// 过程引用指向产生该失败消息的运行并携带其模型用量。
		if message.AgentProcess != nil {
			var usage agentcontract.Usage
			require.NoError(t, json.Unmarshal(runs[message.ID].Usage, &usage))
			require.Equal(t, runs[message.ID].ID, message.AgentProcess.ID)
			require.NotZero(t, usage.TotalTokens)
			require.Equal(t, usage, message.AgentProcess.Usage)
		}
		cursor := &conversationaction.MessageCursorPoint{ID: message.ID, MessageSeq: message.MessageSeq}
		earlier, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Before: cursor})
		require.NoError(t, err)
		require.NotNil(t, earlier.After, "error message pagination")
		later, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, After: earlier.After})
		require.NoError(t, err)
		require.NotEmpty(t, later.Messages, "error message incremental page")
		require.Equal(t, message.ID, later.Messages[0].ID)
	}
	require.Equal(t, 2, count, "failure message count")
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		pending, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		for _, message := range claimed.Messages {
			require.False(t, failures[message.ID], "failure entered model context")
		}
		return agentruntime.RunResult{EndSeq: claimed.EndSeq, Content: "恢复后的回复"}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: nextRunID}))
	restored, err := query.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	count = arr.Count(restored.Messages, func(message conversationaction.ConversationMessage) bool {
		return message.Type == domain.MessageTypeAgentError
	})
	require.Equal(t, 2, count, "next reply replaced error messages")
}

// testCustomerFailureMessage 验证开始执行前失败的客服运行转交人工：错误消息只在成员历史中可见，访客只看到对客通知，重复收尾各类消息只写一次。
func testCustomerFailureMessage(t *testing.T, db *bun.DB, identity *servermodels.Identity, tasks *servertest.Tasks, agentID string) {
	t.Helper()
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "首响失败验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	disableAutoAssignment(t, db, identity.Workspace.ID)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{})
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "首条客服消息"}

	sent, err := receive.Execute(ctx, input)
	require.NoError(t, err)
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", sent.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx))
	executor := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	for range 2 {
		require.NoError(t, executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("model failure details")))
	}
	require.NoError(t, db.NewSelect().Model(&run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusFailed), run.Status)
	require.NotNil(t, run.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeHandoff), *run.Outcome)
	require.NotNil(t, run.OutcomeReason)
	require.Equal(t, string(domain.AgentHandoffReasonRuntimeFailed), *run.OutcomeReason)
	require.NotNil(t, run.HandoffSettledSeq)
	require.Equal(t, int64(1), *run.HandoffSettledSeq)
	var session servermodels.ServiceSession
	require.NoError(t, db.NewSelect().Model(&session).Where("ss.id = ?", run.ScopeID).Scan(ctx))
	require.Nil(t, session.AssigneeIdentityID)
	require.Nil(t, session.TeamID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status)

	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: sent.Conversation.ID})
	require.NoError(t, err)
	counts := map[domain.MessageType]int{}
	for _, message := range history.Messages {
		counts[message.Type]++
		switch message.Type {
		case domain.MessageTypeAgentError:
			require.Equal(t, domain.MessageVisibilityInternal, message.Visibility, "agent error visibility")
		case domain.MessageTypeSystem:
			event := message.SystemEvent
			require.NotNil(t, event)
			require.Equal(t, domain.ConversationSystemEventServiceSessionHandedOff, event.Type)
			require.NotNil(t, event.Reason)
			require.Equal(t, domain.AgentHandoffReasonRuntimeFailed, *event.Reason)
			require.NotNil(t, event.Target)
			require.Equal(t, domain.ServiceSessionTargetPublicQueue, event.Target.Kind)
			require.NotNil(t, event.ReasonText)
			require.Equal(t, "model failure details", *event.ReasonText)
			require.NotNil(t, event.AgentRunID)
			require.Equal(t, run.ID, *event.AgentRunID)
		}
	}
	last := history.Messages[len(history.Messages)-1]
	require.Equal(t, 1, counts[domain.MessageTypeAgentError])
	require.Equal(t, 1, counts[domain.MessageTypeSystem])
	require.Equal(t, 2, counts[domain.MessageTypeText])
	require.Equal(t, *run.ResponseMessageID, last.ID)
	require.Equal(t, domain.MessageTypeText, last.Type)
	require.Nil(t, last.AgentProcess)

	visibleDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(ctx, input.ChannelID, input.ExternalID)
	require.NoError(t, err)
	visible := visibleDirectory.Conversations
	require.Len(t, visible, 1)
	require.Equal(t, last.Body, visible[0].Preview)
	messages, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: input.ChannelID, ExternalID: input.ExternalID, ConversationID: sent.Conversation.ID})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 2)
	require.Equal(t, last.ID, messages.Messages[1].ID)
}
