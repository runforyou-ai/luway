//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// titleReplyRuntime 领取全部待处理输入并返回固定回复。
type titleReplyRuntime struct{}

// Run 领取最新输入并返回固定回复。
func (titleReplyRuntime) Run(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
	triggers, err := feed.Peek(ctx, 0)
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
	db, identity, providerID, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "出差助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "安排出差",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(),
		AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "你好，帮我整理下周三去上海出差的行程",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := first.Conversation.ID
	execute := agentrunaction.NewExecuteAction(db, tasks, titleReplyRuntime{}, testAttachmentReader(db), nil, nil)
	// 同步执行排队中的运行并返回回复消息编号。
	runNext := func() string {
		t.Helper()
		run := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if err := execute.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(run).WherePK().Scan(ctx); err != nil || run.ResponseMessageID == nil {
			t.Fatalf("run=%+v err=%v", run, err)
		}
		return *run.ResponseMessageID
	}
	// 统计本会话已投递的标题任务。
	titleTasks := func() []servermodels.TaskRun {
		t.Helper()
		var runs []servermodels.TaskRun
		if err := db.NewSelect().Model(&runs).
			Where("tr.action_name = ? AND tr.payload->>'conversationId' = ?", agentrunaction.AgentChatTitleActionName, conversationID).
			Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return runs
	}
	replyID := runNext()
	if runs := titleTasks(); len(runs) != 1 || !strings.Contains(string(runs[0].Payload), replyID) {
		t.Fatalf("title tasks after first reply = %+v", runs)
	}
	if _, err := directchataction.NewSendAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "再订一间酒店",
	}); err != nil {
		t.Fatal(err)
	}
	runNext()
	if runs := titleTasks(); len(runs) != 1 {
		t.Fatalf("title tasks after second reply = %d", len(runs))
	}

	// 读取会话当前标题与版本。
	loadConversation := func() *servermodels.Conversation {
		t.Helper()
		conversation := &servermodels.Conversation{}
		if err := db.NewSelect().Model(conversation).Where("cv.id = ?", conversationID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return conversation
	}
	before := loadConversation()
	var input agentrunaction.AgentChatTitleInput
	if err := json.Unmarshal(titleTasks()[0].Payload, &input); err != nil {
		t.Fatal(err)
	}
	if input.ExpectedTitle == nil || before.Title == nil || *input.ExpectedTitle != *before.Title {
		t.Fatalf("expected title = %v, current = %v", input.ExpectedTitle, before.Title)
	}
	// 无效输出按失败重试，不写入标题。
	for _, text := range []string{"标题：上海出差", `{"title":"` + strings.Repeat("长", 41) + `"}`, `{"title":"上海\n出差"}`} {
		if err := agentrunaction.NewGenerateAgentChatTitleAction(db, &summaryCaller{text: text}).Execute(ctx, input); err == nil {
			t.Fatalf("invalid title output %q accepted", text)
		}
	}
	caller := &summaryCaller{text: "好的，标题如下：\n```json\n{\"title\":\"上海出差行程安排。\"}\n```"}
	title := agentrunaction.NewGenerateAgentChatTitleAction(db, caller)
	if err := title.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	// 资料截至首条回复，不包含之后的消息。
	if caller.calls != 1 || !strings.Contains(caller.input, "上海出差的行程") || !strings.Contains(caller.input, `"sender":"ai"`) || strings.Contains(caller.input, "再订一间酒店") {
		t.Fatalf("title input = %s", caller.input)
	}
	after := loadConversation()
	if after.Title == nil || *after.Title != "上海出差行程安排" || after.Version <= before.Version {
		t.Fatalf("title = %v version %d -> %d", after.Title, before.Version, after.Version)
	}
	// 已写入后重跑同一任务时跳过模型调用，标题和版本保持不变。
	caller.text = `{"title":"另一个标题"}`
	if err := title.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if rerun := loadConversation(); caller.calls != 1 || *rerun.Title != "上海出差行程安排" || rerun.Version != after.Version {
		t.Fatalf("rerun calls = %d title = %s version = %d", caller.calls, *rerun.Title, rerun.Version)
	}
	// 回复消息不存在或 AI 员工已停用时任务成功结束，保留标题且不调用模型。
	missing := input
	missing.MessageID, missing.ExpectedTitle = uuid.NewV7().String(), after.Title
	if err := title.Execute(ctx, missing); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", created.IdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	inactive := input
	inactive.ExpectedTitle = after.Title
	if err := title.Execute(ctx, inactive); err != nil {
		t.Fatal(err)
	}
	if final := loadConversation(); caller.calls != 1 || *final.Title != "上海出差行程安排" || final.Version != after.Version {
		t.Fatalf("skipped calls = %d title = %s version = %d", caller.calls, *final.Title, final.Version)
	}
}
