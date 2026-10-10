//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// titleReplyRuntime 领取全部待处理输入并返回固定回复。
type titleReplyRuntime struct{}

// Run 领取最新输入并返回固定回复。
func (titleReplyRuntime) Run(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
	triggers, err := pendingTriggers(ctx, feed, 0)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	return agentruntime.RunResult{Content: "好的，我来安排", EndSeq: claimed.EndSeq}, nil
}

// TestAgentChatTitles 验证 AI 聊天只在首条文本回复后投递标题任务，任务按对话开头生成标题并推进会话版本，无效输出不写入，已写入、回复不存在或 AI 员工停用时跳过模型调用。
func TestAgentChatTitles(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "出差助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: modelID, SystemInstruction: "安排出差",
		}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(),
		AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "你好，帮我整理下周三去上海出差的行程",
	})
	require.NoError(t, err)
	conversationID := first.Conversation.ID
	execute := newTestAgentRun(db, tasks, titleReplyRuntime{}, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	// 同步执行排队中的运行并返回回复消息编号。
	runNext := func() string {
		t.Helper()
		run := &servermodels.AgentRun{}
		require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
		require.NoError(t, execute.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
		require.NoError(t, db.NewSelect().Model(run).WherePK().Scan(ctx))
		require.NotNil(t, run.ResponseMessageID)
		return *run.ResponseMessageID
	}
	// 统计本会话已投递的标题任务。
	titleTasks := func() []servertest.Task {
		t.Helper()
		runs := make([]servertest.Task, 0)
		for _, task := range tasks.Queued(agentrunaction.AgentChatTitleActionName, "") {
			if servertest.TaskPayload[agentrunaction.AgentChatTitleInput](t, task).ConversationID == conversationID {
				runs = append(runs, task)
			}
		}
		return runs
	}
	replyID := runNext()
	runs := titleTasks()
	require.Len(t, runs, 1)
	require.Contains(t, string(runs[0].Payload), replyID)
	_, err = directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "再订一间酒店",
	})
	require.NoError(t, err)
	runNext()
	require.Len(t, titleTasks(), 1, "title tasks after second reply")

	// 读取会话当前标题与版本。
	loadConversation := func() *servermodels.Conversation {
		t.Helper()
		conversation := &servermodels.Conversation{}
		require.NoError(t, db.NewSelect().Model(conversation).Where("cv.id = ?", conversationID).Scan(ctx))
		return conversation
	}
	before := loadConversation()
	var input agentrunaction.AgentChatTitleInput
	require.NoError(t, json.Unmarshal(titleTasks()[0].Payload, &input))
	require.NotNil(t, input.ExpectedTitle)
	require.NotNil(t, before.Title)
	require.Equal(t, *before.Title, *input.ExpectedTitle)
	// 无效输出按失败重试，不写入标题。
	for _, text := range []string{"标题：上海出差", `{"title":"` + strings.Repeat("长", 41) + `"}`, `{"title":"上海\n出差"}`} {
		require.Error(t, agentrunaction.NewGenerateAgentChatTitleAction(db, chatInvoker(db, (&summaryCaller{text: text}).chat)).Execute(ctx, input), "invalid title output %q accepted", text)
	}
	caller := &summaryCaller{text: "好的，标题如下：\n```json\n{\"title\":\"上海出差行程安排。\"}\n```"}
	title := agentrunaction.NewGenerateAgentChatTitleAction(db, chatInvoker(db, caller.chat))
	require.NoError(t, title.Execute(ctx, input))
	// 资料截至首条回复，不包含之后的消息。
	require.Equal(t, 1, caller.calls)
	require.Contains(t, caller.input, "上海出差的行程")
	require.Contains(t, caller.input, `"sender":"ai"`)
	require.NotContains(t, caller.input, "再订一间酒店")
	after := loadConversation()
	require.NotNil(t, after.Title)
	require.Equal(t, "上海出差行程安排", *after.Title)
	require.Greater(t, after.Version, before.Version)
	// 已写入后重跑同一任务时跳过模型调用，标题和版本保持不变。
	caller.text = `{"title":"另一个标题"}`
	require.NoError(t, title.Execute(ctx, input))
	rerun := loadConversation()
	require.Equal(t, 1, caller.calls)
	require.Equal(t, "上海出差行程安排", *rerun.Title)
	require.Equal(t, after.Version, rerun.Version)
	// 回复消息不存在或 AI 员工已停用时任务成功结束，保留标题且不调用模型。
	missing := input
	missing.MessageID, missing.ExpectedTitle = uuid.NewV7().String(), after.Title
	require.NoError(t, title.Execute(ctx, missing))
	_, err = db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", created.IdentityID).Exec(ctx)
	require.NoError(t, err)
	inactive := input
	inactive.ExpectedTitle = after.Title
	require.NoError(t, title.Execute(ctx, inactive))
	final := loadConversation()
	require.Equal(t, 1, caller.calls)
	require.Equal(t, "上海出差行程安排", *final.Title)
	require.Equal(t, after.Version, final.Version)
}
