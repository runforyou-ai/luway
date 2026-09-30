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

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

type groupAgentFixture struct {
	db        *bun.DB
	identity  *servermodels.Identity
	groupID   string
	agents    []*agentaction.Agent
	subjectID map[string]string
	tasks     *servertask.Runtime
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
				ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "协助群内成员",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	detail, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	subjectID := make(map[string]string, len(detail.Participants))
	for _, participant := range detail.Participants {
		subjectID[participant.IdentityID] = participant.ChatSubjectID
	}
	// 群协作用例会产生较多会话与运行记录，用完即清理，避免拖慢共享测试库上的收件箱查询。
	t.Cleanup(func() {
		cleanup := context.Background()
		laneIDs := make([]string, 0)
		if err := db.NewSelect().Model((*servermodels.AgentLane)(nil)).ColumnExpr("al.id").
			Where("al.conversation_id = ?", group.ID).Scan(cleanup, &laneIDs); err != nil {
			t.Error(err)
			return
		}
		if len(laneIDs) > 0 {
			if _, err := db.NewDelete().Table("agent_inputs").Where("lane_id IN (?)", bun.In(laneIDs)).Exec(cleanup); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.NewDelete().Table("message_mentions").
			Where("message_id IN (SELECT id FROM messages WHERE conversation_id = ?)", group.ID).Exec(cleanup); err != nil {
			t.Error(err)
		}
		if _, err := db.NewDelete().Table("agent_run_blocks").
			Where("agent_run_id IN (SELECT id FROM agent_runs WHERE conversation_id = ?)", group.ID).Exec(cleanup); err != nil {
			t.Error(err)
		}
		for _, table := range []string{"agent_runs", "agent_lanes", "messages", "conversation_user_states", "conversation_participants"} {
			if _, err := db.NewDelete().TableExpr(table).Where("conversation_id = ?", group.ID).Exec(cleanup); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.NewDelete().Table("conversations").Where("id = ?", group.ID).Exec(cleanup); err != nil {
			t.Error(err)
		}
	})
	tasks := newGroupAgentTasks(db)
	scheduler := agentrunaction.NewScheduler(tasks)
	return groupAgentFixture{
		db: db, identity: identity, groupID: group.ID, agents: agents, subjectID: subjectID,
		tasks: tasks, scheduler: scheduler,
		send: groupchataction.NewSendGroupTextMessageAction(db, scheduler),
	}
}

// post 发送一条群消息，可携带提醒目标和引用。
func (f groupAgentFixture) post(t *testing.T, body string, mentionIdentityIDs []string, replyToMessageID string) conversationaction.ConversationMessage {
	t.Helper()
	subjects := make([]string, 0, len(mentionIdentityIDs))
	for _, identityID := range mentionIdentityIDs {
		subjects = append(subjects, f.subjectID[identityID])
	}
	message, err := f.send.Execute(context.Background(), f.identity, groupchataction.GroupTextMessageInput{
		ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: body,
		MentionSubjectIDs: subjects, ReplyToMessageID: replyToMessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

// activeRun 读取群内当前排队或运行中的 Agent 运行。
func (f groupAgentFixture) activeRun(t *testing.T) *servermodels.AgentRun {
	t.Helper()
	runs := make([]servermodels.AgentRun, 0)
	if err := f.db.NewSelect().Model(&runs).
		Where("agr.conversation_id = ?", f.groupID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runs) > 1 {
		t.Fatalf("群内同时存在 %d 个活动运行，期望至多 1 个", len(runs))
	}
	if len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

// runNext 用可控 Runtime 执行群内当前排队的运行，并返回执行的 Agent 身份。
func (f groupAgentFixture) runNext(t *testing.T, reply string, inspect func(agentruntime.ClaimedInput)) string {
	t.Helper()
	ctx := context.Background()
	run := f.activeRun(t)
	if run == nil {
		t.Fatal("群内没有待执行的运行")
	}
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		triggers, err := feed.Peek(ctx, 0)
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
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewSelect().Model(run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusSucceeded) {
		t.Fatalf("执行未成功：%+v", run)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		if _, err := markFileUploaded(ctx, db, identity, file.ID, ""); err != nil {
			t.Fatal(err)
		}
		attachment, err := directchataction.NewSendAttachmentMessageAction(db, nil).Execute(ctx, identity, directchataction.AttachmentMessageInput{
			ConversationID: f.groupID, FileID: file.ID, ClientMessageID: uuid.NewV7().String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if run := f.activeRun(t); run != nil {
			t.Fatalf("群附件消息不应触发执行：%+v", run)
		}
		f.post(t, "请看这张图", []string{f.agents[0].IdentityID}, attachment.Message.ID)
		f.runNext(t, "图已看到", func(claimed agentruntime.ClaimedInput) {
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
				if err := json.Unmarshal([]byte(message.Content), &envelope); err != nil {
					t.Fatalf("群上下文不是 JSON：%s", message.Content)
				}
				if message.ID == attachment.Message.ID {
					attached = envelope.Attachment != nil && envelope.Attachment.MessageID == attachment.Message.ID &&
						envelope.Attachment.Name == "diagram.png" && strings.Contains(envelope.Attachment.URL, "/storage/") && message.Media != nil && message.Media.MIMEType == "image/png"
				}
				if envelope.ReplyTo != nil && envelope.ReplyTo.Attachment != nil && envelope.ReplyTo.Attachment.Name == "diagram.png" && envelope.ReplyTo.Attachment.ByteSize == domain.FilePartSize+1 {
					referenced = true
				}
			}
			if !attached || !referenced {
				t.Fatalf("群附件上下文 = %+v", claimed.Messages)
			}
		})
	})

	t.Run("点名触发与上下文发送者", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "先随便聊两句", nil, "")
		if run := f.activeRun(t); run != nil {
			t.Fatalf("普通群消息不应触发执行：%+v", run)
		}
		f.post(t, "请帮忙看下这个方案", []string{f.agents[0].IdentityID}, "")
		run := f.activeRun(t)
		if run == nil || run.AgentIdentityID != f.agents[0].IdentityID {
			t.Fatalf("点名后活动运行 = %+v", run)
		}
		var senders []string
		f.runNext(t, "方案没有问题", func(claimed agentruntime.ClaimedInput) {
			for _, message := range claimed.Messages {
				if message.Role != agentruntime.MessageRoleUser {
					t.Fatalf("他人发言应投影为 user 角色：%+v", message)
				}
				var envelope struct {
					Sender struct {
						Name string `json:"name"`
						Kind string `json:"kind"`
					} `json:"sender"`
					Body string `json:"body"`
				}
				if err := json.Unmarshal([]byte(message.Content), &envelope); err != nil {
					t.Fatalf("群上下文正文不是结构化封装：%q", message.Content)
				}
				if envelope.Sender.Name == "" || envelope.Body == "" {
					t.Fatalf("群上下文缺少发送者或正文：%q", message.Content)
				}
				if strings.Contains(message.Content, "identityId") {
					t.Fatalf("群上下文不应包含身份编号：%q", message.Content)
				}
				senders = append(senders, envelope.Sender.Name)
			}
		})
		if len(senders) != 2 {
			t.Fatalf("模型上下文消息数量 = %d，期望 2", len(senders))
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil {
			t.Fatal(err)
		}
		last := history.Messages[len(history.Messages)-1]
		if last.Body != "方案没有问题" || last.Sender == nil || last.Sender.SourceID != f.agents[0].IdentityID {
			t.Fatalf("群内 AI 回复 = %+v", last)
		}
		if run := f.activeRun(t); run != nil {
			t.Fatalf("回复后仍有活动运行：%+v", run)
		}
	})

	t.Run("系统提示词包含身份与群聊场景", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请帮忙看一下", []string{f.agents[0].IdentityID}, "")
		run := f.activeRun(t)
		captured := ""
		runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			captured = request.Assignment.Instruction
			triggers, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			return agentruntime.RunResult{Content: "收到", EndSeq: claimed.EndSeq}, nil
		}}
		if err := agentrunaction.NewExecuteAction(db, f.tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{f.agents[0].DisplayName, "AI 协作群", "sender.name", "addressedToYou", "协助群内成员"} {
			if !strings.Contains(captured, fragment) {
				t.Fatalf("系统提示词缺少 %q：%q", fragment, captured)
			}
		}
	})

	t.Run("引用回复等价于点名", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "第一次提问", []string{f.agents[0].IdentityID}, "")
		f.runNext(t, "第一次回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil {
			t.Fatal(err)
		}
		agentMessageID := history.Messages[len(history.Messages)-1].ID
		f.post(t, "再展开讲讲", nil, agentMessageID)
		run := f.activeRun(t)
		if run == nil || run.AgentIdentityID != f.agents[0].IdentityID {
			t.Fatalf("引用 AI 回复后的活动运行 = %+v", run)
		}
	})

	t.Run("多个目标按顺序轮转", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		first := f.runNext(t, "第一位的意见", func(claimed agentruntime.ClaimedInput) {
			// 同一条消息点名多位时，各方都应知道本轮还有谁参与。
			last := claimed.Messages[len(claimed.Messages)-1]
			for _, agent := range agents {
				if !strings.Contains(last.Content, agent.DisplayName) {
					t.Fatalf("上下文缺少同轮被点名成员 %q：%q", agent.DisplayName, last.Content)
				}
			}
		})
		second := f.runNext(t, "第二位的意见", func(claimed agentruntime.ClaimedInput) {
			// 串行执行下，后发言者读到前一位已提交的回复，且能区分本次要处理的请求。
			seen, addressed := false, 0
			for _, message := range claimed.Messages {
				if strings.Contains(message.Content, "第一位的意见") {
					seen = true
					if strings.Contains(message.Content, "addressedToYou") {
						t.Fatalf("其他 AI 员工的发言不应标记为本次请求：%q", message.Content)
					}
				}
				if strings.Contains(message.Content, `"addressedToYou":true`) {
					addressed++
				}
			}
			if !seen {
				t.Fatalf("后发言者上下文缺少前一位的回复：%+v", claimed.Messages)
			}
			if addressed != 1 {
				t.Fatalf("本次需要处理的消息数量 = %d，期望 1", addressed)
			}
		})
		if first == second {
			t.Fatalf("两次运行属于同一 Agent：%s", first)
		}
		if run := f.activeRun(t); run != nil {
			t.Fatalf("轮转结束后仍有活动运行：%+v", run)
		}
		spoken := map[string]bool{first: true, second: true}
		for _, agent := range f.agents {
			if !spoken[agent.IdentityID] {
				t.Fatalf("AI 员工 %s 没有发言", agent.IdentityID)
			}
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
		if run == nil || run.AgentIdentityID != ordered[0] {
			t.Fatalf("首个执行者 = %+v，期望点名顺序中的第一位", run)
		}
		if input := f.latestInput(t, run.LaneID); input.SourceOrdinal != 0 {
			t.Fatalf("首个目标的顺序号 = %d，期望 0", input.SourceOrdinal)
		}
		first := f.runNext(t, "第一位的意见", nil)
		second := f.activeRun(t)
		if second == nil || second.AgentIdentityID != ordered[1] || first != ordered[0] {
			t.Fatalf("轮转顺序与点名顺序不一致：first=%s next=%+v", first, second)
		}
	})

	t.Run("停止后轮转继续", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
		status, err := coordinator.StopGroupAgentReply(ctx, identity, f.groupID, running.ID)
		if err != nil || status != domain.AgentRunStatusCancelled {
			t.Fatalf("停止群内运行 status=%s err=%v", status, err)
		}
		next := f.activeRun(t)
		if next == nil || next.AgentIdentityID == running.AgentIdentityID {
			t.Fatalf("停止后未轮转到另一位 AI 员工：%+v", next)
		}
	})

	t.Run("移出群成员收敛执行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
		if _, err := groupchataction.NewRemoveGroupConversationMemberAction(db, coordinator).Execute(ctx, identity, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupID, MemberIdentityID: running.AgentIdentityID,
		}); err != nil {
			t.Fatal(err)
		}
		cancelled := &servermodels.AgentRun{}
		if err := f.db.NewSelect().Model(cancelled).Where("agr.id = ?", running.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != string(domain.AgentRunStatusCancelled) || cancelled.ErrorCode == nil ||
			*cancelled.ErrorCode != string(domain.AgentRunErrorCodeAgentRemoved) {
			t.Fatalf("移出后的运行 = %+v", cancelled)
		}
		lane := &servermodels.AgentLane{}
		if err := f.db.NewSelect().Model(lane).Where("al.id = ?", running.LaneID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if lane.ProcessedSeq != lane.DesiredSeq {
			t.Fatalf("移出后的输入队列未结算：%+v", lane)
		}
		next := f.activeRun(t)
		if next == nil || next.AgentIdentityID == running.AgentIdentityID {
			t.Fatalf("移出正在执行的 AI 员工后未轮转到另一位：%+v", next)
		}
	})

	t.Run("失效运行收敛推进会话版本", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请看下", []string{f.agents[0].IdentityID}, "")
		running := f.activeRun(t)
		// 绕过成员操作直接移出 AI 员工，模拟运行写回前失去执行资格。
		if _, err := db.NewUpdate().Table("conversation_participants").Set("left_at = now()").
			Where("conversation_id = ? AND subject_id IN (SELECT id FROM chat_subjects WHERE source_id = ?)", f.groupID, running.AgentIdentityID).
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
		feed := startRealtimeFeed(t, identity.Organization.ID)
		before := loadConversationVersion(t, db, f.groupID)
		coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
		if err := coordinator.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: running.ID}, errors.New("late failure")); err != nil {
			t.Fatal(err)
		}
		cancelled := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(cancelled).Where("agr.id = ?", running.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != string(domain.AgentRunStatusCancelled) || cancelled.ErrorCode == nil ||
			*cancelled.ErrorCode != string(domain.AgentRunErrorCodeAgentRemoved) {
			t.Fatalf("失效运行 = %+v", cancelled)
		}
		after := loadConversationVersion(t, db, f.groupID)
		if after != before+1 {
			t.Fatalf("收敛后的会话版本=%d want=%d", after, before+1)
		}
		feed.expect(t, feed.notice(identity.User.ID, realtime.KindConversationChanged, f.groupID, after))
	})

	t.Run("解散群取消全部在途运行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
		if _, err := groupchataction.NewDissolveGroupConversationAction(db, coordinator).Execute(ctx, identity, f.groupID); err != nil {
			t.Fatal(err)
		}
		if run := f.activeRun(t); run != nil {
			t.Fatalf("解散后仍有活动运行：%+v", run)
		}
		lanes := make([]servermodels.AgentLane, 0)
		if err := db.NewSelect().Model(&lanes).Where("al.conversation_id = ?", f.groupID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		for _, lane := range lanes {
			if lane.ProcessedSeq != lane.DesiredSeq {
				t.Fatalf("解散后输入队列未结算：%+v", lane)
			}
		}
	})

	t.Run("引用目标先于点名目标执行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "先问第二位", []string{f.agents[1].IdentityID}, "")
		f.runNext(t, "第二位先回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil {
			t.Fatal(err)
		}
		secondReplyID := history.Messages[len(history.Messages)-1].ID
		f.post(t, "顺带请第一位也看下", []string{f.agents[0].IdentityID}, secondReplyID)
		run := f.activeRun(t)
		if run == nil || run.AgentIdentityID != f.agents[1].IdentityID {
			t.Fatalf("引用目标未先于点名目标执行：%+v", run)
		}
	})

	t.Run("同一目标的引用与点名合并为一条输入", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "第一次提问", []string{f.agents[0].IdentityID}, "")
		f.runNext(t, "第一次回答", nil)
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil {
			t.Fatal(err)
		}
		replyID := history.Messages[len(history.Messages)-1].ID
		message := f.post(t, "再补充一点", []string{f.agents[0].IdentityID}, replyID)
		count, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).
			Where("ai.source_message_id = ?", message.ID).Count(ctx)
		if err != nil || count != 1 {
			t.Fatalf("同一目标的引用与点名产生 %d 条输入，期望 1 条，error = %v", count, err)
		}
	})

	t.Run("停用不取消已提交输入", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		waiting, waitingAgentID := f.agents[0].IdentityID, f.agents[0].ID
		if waiting == running.AgentIdentityID {
			waiting, waitingAgentID = f.agents[1].IdentityID, f.agents[1].ID
		}
		if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(ctx, identity, waitingAgentID, domain.IdentityStatusInactive); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(context.Background(), identity, waitingAgentID, domain.IdentityStatusActive); err != nil {
				t.Error(err)
			}
		})
		f.runNext(t, "第一位的回答", nil)
		next := f.activeRun(t)
		if next == nil || next.AgentIdentityID != waiting {
			t.Fatalf("停用的 AI 员工的已提交输入未继续处理：%+v", next)
		}
	})

	t.Run("移出排队中的成员后轮转", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		running := f.activeRun(t)
		waiting := f.agents[0].IdentityID
		if waiting == running.AgentIdentityID {
			waiting = f.agents[1].IdentityID
		}
		coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
		if _, err := groupchataction.NewRemoveGroupConversationMemberAction(db, coordinator).Execute(ctx, identity, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupID, MemberIdentityID: waiting,
		}); err != nil {
			t.Fatal(err)
		}
		if current := f.activeRun(t); current == nil || current.ID != running.ID {
			t.Fatalf("移出排队中的成员不应影响当前运行：%+v", current)
		}
		f.runNext(t, "第一位的回答", nil)
		if run := f.activeRun(t); run != nil {
			t.Fatalf("被移出成员的输入已结算，不应再轮转：%+v", run)
		}
	})

	t.Run("执行失败后轮转继续", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		failing := f.activeRun(t)
		runtime := testAgentRuntime{run: func(context.Context, agentruntime.RunRequest, agentruntime.InputFeed) (agentruntime.RunResult, error) {
			return agentruntime.RunResult{}, errors.New("模型不可用")
		}}
		if err := agentrunaction.NewExecuteAction(db, f.tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: failing.ID}); err == nil {
			t.Fatal("期望执行返回失败")
		}
		next := f.activeRun(t)
		if next == nil || next.AgentIdentityID == failing.AgentIdentityID {
			t.Fatalf("失败后未轮转到另一位 AI 员工：%+v", next)
		}
	})

	t.Run("排队中的 AI 员工可读", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位一起看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil {
			t.Fatal(err)
		}
		if len(history.PendingAgents) != 1 {
			t.Fatalf("排队中的 AI 员工 = %+v，期望 1 位", history.PendingAgents)
		}
		running := f.activeRun(t)
		if running == nil || history.PendingAgents[0].IdentityID == running.AgentIdentityID {
			t.Fatalf("排队的 AI 员工与当前运行重叠：%+v", history.PendingAgents)
		}
	})
}
