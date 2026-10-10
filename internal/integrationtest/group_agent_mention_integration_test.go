//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// groupAgentFixture 是群聊提及 AI 员工的测试环境。
type groupAgentFixture struct {
	db        *bun.DB
	identity  *servermodels.Identity
	groupID   string
	agents    []*agentaction.Agent
	subjectID map[string]string
	tasks     *servertest.Tasks
	scheduler *agentrunaction.Scheduler
	send      *groupchataction.SendGroupTextMessageAction
}

// newGroupAgentCollaborators 创建供群协作用例共享的两位 AI 员工。
func newGroupAgentCollaborators(t *testing.T, db *bun.DB, identity *servermodels.Identity, providerID, modelID string) []*agentaction.Agent {
	t.Helper()
	ctx := context.Background()
	agents := make([]*agentaction.Agent, 0, 2)
	for i := range 2 {
		agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
			DisplayName: fmt.Sprintf("群协作助手 %d", i),
			Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
				ModelID: modelID, SystemInstruction: "协助群内成员",
			}},
		})
		require.NoError(t, err)
		agents = append(agents, agent)
	}
	return agents
}

// newGroupAgentFixture 用共享的 AI 员工建立独立测试群聊。
func newGroupAgentFixture(t *testing.T, db *bun.DB, identity *servermodels.Identity, agents []*agentaction.Agent) groupAgentFixture {
	t.Helper()
	ctx := context.Background()
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "AI 协作群", MemberIdentityIDs: []string{agents[0].IdentityID, agents[1].IdentityID},
	})
	require.NoError(t, err)
	detail, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
	require.NoError(t, err)
	subjectID := arr.Associate(detail.Participants, func(participant groupchataction.GroupParticipant) (string, string) {
		return participant.IdentityID, participant.ChatSubjectID
	})
	// 群协作用例会产生较多会话与运行记录，用完即清理，保持共享测试库上收件箱查询的速度。
	t.Cleanup(func() {
		cleanup := context.Background()
		laneIDs := make([]string, 0)
		if !assert.NoError(t, db.NewSelect().Model((*servermodels.AgentLane)(nil)).ColumnExpr("al.id").
			Where("al.conversation_id = ?", group.ID).Scan(cleanup, &laneIDs)) {
			return
		}
		if len(laneIDs) > 0 {
			_, err := db.NewDelete().Table("agent_inputs").Where("lane_id IN (?)", bun.List(laneIDs)).Exec(cleanup)
			assert.NoError(t, err)
		}
		_, err := db.NewDelete().Table("message_mentions").
			Where("message_id IN (SELECT id FROM messages WHERE conversation_id = ?)", group.ID).Exec(cleanup)
		assert.NoError(t, err)
		_, err = db.NewDelete().Table("agent_run_blocks").
			Where("agent_run_id IN (SELECT id FROM agent_runs WHERE conversation_id = ?)", group.ID).Exec(cleanup)
		assert.NoError(t, err)
		for _, table := range []string{"agent_runs", "agent_lanes", "messages", "conversation_user_states", "conversation_participants"} {
			_, err := db.NewDelete().TableExpr(table).Where("conversation_id = ?", group.ID).Exec(cleanup)
			assert.NoError(t, err)
		}
		_, err = db.NewDelete().Table("conversations").Where("id = ?", group.ID).Exec(cleanup)
		assert.NoError(t, err)
	})
	tasks := newGroupAgentTasks(db)
	scheduler := agentrunaction.NewScheduler(tasks)
	return groupAgentFixture{
		db: db, identity: identity, groupID: group.ID, agents: agents, subjectID: subjectID,
		tasks: tasks, scheduler: scheduler,
		send: groupchataction.NewSendGroupTextMessageAction(db, testEnqueuer, scheduler),
	}
}

// post 发送一条群消息，可携带提醒目标和引用。
func (f groupAgentFixture) post(t *testing.T, body string, mentionIdentityIDs []string, replyToMessageID string) conversationaction.ConversationMessage {
	t.Helper()
	subjects := arr.Map(mentionIdentityIDs, func(identityID string) string { return f.subjectID[identityID] })
	message, err := f.send.Execute(context.Background(), f.identity, groupchataction.GroupTextMessageInput{
		ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: body,
		MentionSubjectIDs: subjects, ReplyToMessageID: replyToMessageID,
	})
	require.NoError(t, err)
	return message
}

// activeRun 读取群内当前排队或运行中的 Agent 运行。
func (f groupAgentFixture) activeRun(t *testing.T) *servermodels.AgentRun {
	t.Helper()
	runs := make([]servermodels.AgentRun, 0)
	require.NoError(t, f.db.NewSelect().Model(&runs).
		Where("agr.conversation_id = ?", f.groupID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(context.Background()))
	require.LessOrEqual(t, len(runs), 1, "群内同时存在多个活动运行")
	if len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

// runNext 用可控 Runtime 执行群内当前排队的运行，并返回执行的 Agent 身份。
func (f groupAgentFixture) runNext(t *testing.T, reply string, inspect func(einorun.Claim)) string {
	t.Helper()
	ctx := context.Background()
	run := f.activeRun(t)
	require.NotNil(t, run, "群内没有待执行的运行")
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if inspect != nil {
			inspect(claimed)
		}
		return agentruntime.RunResult{Content: reply, EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, f.db.NewSelect().Model(run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status, "执行未成功：%+v", run)
	return run.AgentIdentityID
}

// TestGroupAgentMentionReplies 验证群内点名触发、引用等价、轮转顺序与成员变化收敛。
func TestGroupAgentMentionReplies(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	agents := newGroupAgentCollaborators(t, db, identity, providerID, modelID)

	t.Run("群内附件进入上下文", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		file, err := fileaction.NewCreateUploadAction(db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeMessageAttachment, FileName: "diagram.png", ContentType: "image/png", ByteSize: domain.FilePartSize + 1,
		})
		require.NoError(t, err)
		_, err = markFileUploaded(ctx, db, identity, file.ID, "")
		require.NoError(t, err)
		attachment, err := directchataction.NewSendAttachmentMessageAction(db, testEnqueuer, nil).Execute(ctx, identity, directchataction.AttachmentMessageInput{
			ConversationID: f.groupID, FileID: file.ID, ClientMessageID: uuid.NewV7().String(),
		})
		require.NoError(t, err)
		require.Nil(t, f.activeRun(t), "群附件消息不应触发执行")
		f.post(t, "请看这张图", []string{f.agents[0].IdentityID}, attachment.Message.ID)
		f.runNext(t, "图已看到", func(claimed einorun.Claim) {
			var attached, referenced bool
			for _, message := range claimed.Messages {
				var envelope struct {
					Attachment *struct {
						MessageID string `json:"messageId"`
						Name      string `json:"name"`
						URL       string `json:"url"`
					} `json:"attachment"`
					ReplyTo *struct {
						Attachment *struct {
							Name     string `json:"name"`
							ByteSize int64  `json:"byteSize"`
						} `json:"attachment"`
					} `json:"replyTo"`
				}
				require.NoError(t, json.Unmarshal([]byte(message.Content), &envelope), "群上下文不是 JSON：%s", message.Content)
				if message.ID == attachment.Message.ID {
					attached = envelope.Attachment != nil && envelope.Attachment.MessageID == attachment.Message.ID &&
						envelope.Attachment.Name == "diagram.png" && strings.Contains(envelope.Attachment.URL, "/storage/") && message.Media != nil && message.Media.MIME == "image/png"
				}
				if envelope.ReplyTo != nil && envelope.ReplyTo.Attachment != nil && envelope.ReplyTo.Attachment.Name == "diagram.png" && envelope.ReplyTo.Attachment.ByteSize == domain.FilePartSize+1 {
					referenced = true
				}
			}
			require.True(t, attached, "群附件上下文 = %+v", claimed.Messages)
			require.True(t, referenced, "群附件上下文 = %+v", claimed.Messages)
		})
	})

	t.Run("点名触发与上下文发送者", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "先随便聊两句", nil, "")
		require.Nil(t, f.activeRun(t), "普通群消息不应触发执行")
		f.post(t, "请帮忙看下这个方案", []string{f.agents[0].IdentityID}, "")
		run := f.activeRun(t)
		require.NotNil(t, run)
		require.Equal(t, f.agents[0].IdentityID, run.AgentIdentityID)
		var senders []string
		f.runNext(t, "方案没有问题", func(claimed einorun.Claim) {
			for _, message := range claimed.Messages {
				require.Equal(t, einorun.RoleUser, message.Role, "他人发言应投影为 user 角色：%+v", message)
				var envelope struct {
					Sender struct {
						Name string `json:"name"`
						Kind string `json:"kind"`
					} `json:"sender"`
					Body string `json:"body"`
				}
				require.NoError(t, json.Unmarshal([]byte(message.Content), &envelope), "群上下文正文不是结构化封装：%q", message.Content)
				require.NotEmpty(t, envelope.Sender.Name, "群上下文缺少发送者：%q", message.Content)
				require.NotEmpty(t, envelope.Body, "群上下文缺少正文：%q", message.Content)
				require.NotContains(t, message.Content, "identityId", "群上下文不应包含身份编号")
				senders = append(senders, envelope.Sender.Name)
			}
		})
		require.Len(t, senders, 2, "模型上下文消息数量")
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		last := history.Messages[len(history.Messages)-1]
		require.Equal(t, "方案没有问题", last.Body)
		require.NotNil(t, last.Sender)
		require.Equal(t, f.agents[0].IdentityID, last.Sender.SourceID)
		require.Nil(t, f.activeRun(t), "回复后仍有活动运行")
	})

	t.Run("引用回复等价于点名", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "第一次提问", []string{f.agents[0].IdentityID}, "")
		f.runNext(t, "第一次回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		agentMessageID := history.Messages[len(history.Messages)-1].ID
		f.post(t, "再展开讲讲", nil, agentMessageID)
		run := f.activeRun(t)
		require.NotNil(t, run, "引用 AI 回复后的活动运行")
		require.Equal(t, f.agents[0].IdentityID, run.AgentIdentityID, "引用 AI 回复后的活动运行")
	})

	t.Run("多个目标按顺序轮转", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		first := f.runNext(t, "第一位的意见", func(claimed einorun.Claim) {
			// 同一条消息点名多位时，各方都应知道本轮还有谁参与。
			last := claimed.Messages[len(claimed.Messages)-1]
			for _, agent := range agents {
				require.Contains(t, last.Content, agent.DisplayName, "上下文缺少同轮被点名成员")
			}
		})
		second := f.runNext(t, "第二位的意见", func(claimed einorun.Claim) {
			// 串行执行下，后发言者读到前一位已提交的回复，且能区分本次要处理的请求。
			seen, addressed := false, 0
			for _, message := range claimed.Messages {
				if strings.Contains(message.Content, "第一位的意见") {
					seen = true
					require.NotContains(t, message.Content, "addressedToYou", "其他 AI 员工的发言不应标记为本次请求")
				}
				if strings.Contains(message.Content, `"addressedToYou":true`) {
					addressed++
				}
			}
			require.True(t, seen, "后发言者上下文缺少前一位的回复：%+v", claimed.Messages)
			require.Equal(t, 1, addressed, "本次需要处理的消息数量")
		})
		require.NotEqual(t, first, second, "两次运行属于同一 Agent")
		require.Nil(t, f.activeRun(t), "轮转结束后仍有活动运行")
		spoken := map[string]bool{first: true, second: true}
		for _, agent := range f.agents {
			require.True(t, spoken[agent.IdentityID], "AI 员工 %s 没有发言", agent.IdentityID)
		}
	})

	t.Run("发言顺序取发送时的点名顺序", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		// 构造与 chat subject 排序相反的点名顺序，确认发言先后取决于发送顺序。
		ordered := []string{f.agents[0].IdentityID, f.agents[1].IdentityID}
		if f.subjectID[ordered[0]] < f.subjectID[ordered[1]] {
			ordered[0], ordered[1] = ordered[1], ordered[0]
		}
		f.post(t, "两位一起看下", ordered, "")
		run := f.activeRun(t)
		require.NotNil(t, run)
		require.Equal(t, ordered[0], run.AgentIdentityID, "首个执行者应为点名顺序中的第一位")
		require.Zero(t, f.latestInput(t, run.LaneID).SourceOrdinal, "首个目标的顺序号")
		first := f.runNext(t, "第一位的意见", nil)
		second := f.activeRun(t)
		require.NotNil(t, second)
		require.Equal(t, ordered[1], second.AgentIdentityID, "轮转顺序与点名顺序不一致")
		require.Equal(t, ordered[0], first, "轮转顺序与点名顺序不一致")
	})

	t.Run("停止后轮转继续", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		status, err := coordinator.StopGroupAgentReply(ctx, identity, f.groupID, running.ID)
		require.NoError(t, err)
		require.Equal(t, domain.AgentRunStatusCancelled, status)
		next := f.activeRun(t)
		require.NotNil(t, next, "停止后未轮转到另一位 AI 员工")
		require.NotEqual(t, running.AgentIdentityID, next.AgentIdentityID, "停止后未轮转到另一位 AI 员工")
	})

	t.Run("移出群成员收敛执行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, coordinator).Execute(ctx, identity, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupID, MemberIdentityID: running.AgentIdentityID,
		})
		require.NoError(t, err)
		cancelled := &servermodels.AgentRun{}
		require.NoError(t, f.db.NewSelect().Model(cancelled).Where("agr.id = ?", running.ID).Scan(ctx))
		require.Equal(t, string(domain.AgentRunStatusCancelled), cancelled.Status)
		require.NotNil(t, cancelled.ErrorCode)
		require.Equal(t, string(domain.AgentRunErrorCodeAgentRemoved), *cancelled.ErrorCode)
		lane := &servermodels.AgentLane{}
		require.NoError(t, f.db.NewSelect().Model(lane).Where("al.id = ?", running.LaneID).Scan(ctx))
		require.Equal(t, lane.DesiredSeq, lane.ProcessedSeq, "移出后的输入队列未结算")
		next := f.activeRun(t)
		require.NotNil(t, next, "移出正在执行的 AI 员工后未轮转到另一位")
		require.NotEqual(t, running.AgentIdentityID, next.AgentIdentityID, "移出正在执行的 AI 员工后未轮转到另一位")
	})

	t.Run("失效运行收敛推进会话版本", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请看下", []string{f.agents[0].IdentityID}, "")
		running := f.activeRun(t)
		// 绕过成员操作直接移出 AI 员工，模拟运行写回前失去执行资格。
		_, err := db.NewUpdate().Table("conversation_participants").Set("left_at = now()").
			Where("conversation_id = ? AND subject_id IN (SELECT id FROM chat_subjects WHERE source_id = ?)", f.groupID, running.AgentIdentityID).
			Exec(ctx)
		require.NoError(t, err)
		feed := startRealtimeFeed(t, identity.Workspace.ID)
		before := loadConversationVersion(t, db, f.groupID)
		coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		require.NoError(t, coordinator.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: running.ID}, errors.New("late failure")))
		cancelled := &servermodels.AgentRun{}
		require.NoError(t, db.NewSelect().Model(cancelled).Where("agr.id = ?", running.ID).Scan(ctx))
		require.Equal(t, string(domain.AgentRunStatusCancelled), cancelled.Status)
		require.NotNil(t, cancelled.ErrorCode)
		require.Equal(t, string(domain.AgentRunErrorCodeAgentRemoved), *cancelled.ErrorCode)
		after := loadConversationVersion(t, db, f.groupID)
		require.Equal(t, before+1, after, "收敛后的会话版本")
		feed.expect(t, feed.notice(identity.User.ID, realtime.KindConversationChanged, f.groupID, after))
	})

	t.Run("解散群取消全部在途运行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		_, err := groupchataction.NewDissolveGroupConversationAction(db, coordinator).Execute(ctx, identity, f.groupID)
		require.NoError(t, err)
		require.Nil(t, f.activeRun(t), "解散后仍有活动运行")
		lanes := make([]servermodels.AgentLane, 0)
		require.NoError(t, db.NewSelect().Model(&lanes).Where("al.conversation_id = ?", f.groupID).Scan(ctx))
		for _, lane := range lanes {
			require.Equal(t, lane.DesiredSeq, lane.ProcessedSeq, "解散后输入队列未结算：%+v", lane)
		}
	})

	t.Run("引用目标先于点名目标执行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "先问第二位", []string{f.agents[1].IdentityID}, "")
		f.runNext(t, "第二位先回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		secondReplyID := history.Messages[len(history.Messages)-1].ID
		f.post(t, "顺带请第一位也看下", []string{f.agents[0].IdentityID}, secondReplyID)
		run := f.activeRun(t)
		require.NotNil(t, run, "引用目标未先于点名目标执行")
		require.Equal(t, f.agents[1].IdentityID, run.AgentIdentityID, "引用目标未先于点名目标执行")
	})

	t.Run("同一目标的引用与点名合并为一条输入", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "第一次提问", []string{f.agents[0].IdentityID}, "")
		f.runNext(t, "第一次回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		replyID := history.Messages[len(history.Messages)-1].ID
		message := f.post(t, "再补充一点", []string{f.agents[0].IdentityID}, replyID)
		count, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).
			Where("ai.source_message_id = ?", message.ID).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), count, "同一目标的引用与点名产生的输入条数")
	})

	t.Run("停用不取消已提交输入", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		waiting, waitingAgentID := f.agents[0].IdentityID, f.agents[0].ID
		if waiting == running.AgentIdentityID {
			waiting, waitingAgentID = f.agents[1].IdentityID, f.agents[1].ID
		}
		_, err := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, waitingAgentID, domain.IdentityStatusInactive)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), identity, waitingAgentID, domain.IdentityStatusActive)
			assert.NoError(t, err)
		})
		f.runNext(t, "第一位的回答", nil)
		next := f.activeRun(t)
		require.NotNil(t, next, "停用的 AI 员工的已提交输入未继续处理")
		require.Equal(t, waiting, next.AgentIdentityID, "停用的 AI 员工的已提交输入未继续处理")
	})

	t.Run("移出排队中的成员后轮转", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		waiting := f.agents[0].IdentityID
		if waiting == running.AgentIdentityID {
			waiting = f.agents[1].IdentityID
		}
		coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, coordinator).Execute(ctx, identity, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupID, MemberIdentityID: waiting,
		})
		require.NoError(t, err)
		current := f.activeRun(t)
		require.NotNil(t, current, "移出排队中的成员不应影响当前运行")
		require.Equal(t, running.ID, current.ID, "移出排队中的成员不应影响当前运行")
		f.runNext(t, "第一位的回答", nil)
		require.Nil(t, f.activeRun(t), "被移出成员的输入已结算，不应再轮转")
	})

	t.Run("执行失败后轮转继续", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		failing := f.activeRun(t)
		runtime := testAgentRuntime{run: func(context.Context, agentruntime.RunRequest, einorun.Feed) (agentruntime.RunResult, error) {
			return agentruntime.RunResult{}, errors.New("模型不可用")
		}}
		require.Error(t, newTestAgentRun(db, f.tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: failing.ID}), "期望执行返回失败")
		next := f.activeRun(t)
		require.NotNil(t, next, "失败后未轮转到另一位 AI 员工")
		require.NotEqual(t, failing.AgentIdentityID, next.AgentIdentityID, "失败后未轮转到另一位 AI 员工")
	})

	t.Run("排队中的 AI 员工可读", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		require.Len(t, history.PendingAgents, 1, "排队中的 AI 员工")
		running := f.activeRun(t)
		require.NotNil(t, running)
		require.NotEqual(t, running.AgentIdentityID, history.PendingAgents[0].IdentityID, "排队的 AI 员工与当前运行重叠")
	})
}
