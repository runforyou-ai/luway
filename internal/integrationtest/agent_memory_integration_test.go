//go:build server

package integrationtest

import (
	"context"
	"testing"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedMemoryExtractor 记录提取请求，在返回前执行可选的并发操作并返回预设变更。
type scriptedMemoryExtractor struct {
	requests []agentruntime.MemoryExtractionRequest
	during   func()
	result   agentruntime.MemoryExtractionResult
}

// ExtractMemory 记录请求并返回预设变更。
func (e *scriptedMemoryExtractor) ExtractMemory(_ context.Context, request agentruntime.MemoryExtractionRequest) (agentruntime.MemoryExtractionResult, error) {
	e.requests = append(e.requests, request)
	if e.during != nil {
		e.during()
	}
	return e.result, nil
}

// testAgentMemory 验证个人 AI 员工单聊的运行下发记忆、回复后投递提取任务、提取只分析新消息并推进进度，以及负责人查看、编辑、删除记忆；提取期间记忆被负责人修改或进度被其他任务推进时本次结果作废，重试时基于最新状态提取。
func testAgentMemory(t *testing.T, f *personalAgentFixture) {
	ctx, db, identity := f.ctx, f.db, f.identity
	conversationID := f.personalAgentChat()
	run := f.sendAndLoadRun(conversationID, "以后周报按客户分组")
	f.execute(memoryRuntime(t, 0, "好的，以后按客户分组"), run.ID)

	inputs := servertest.QueuedInputs(t, f.tasks, agentrunaction.AgentMemoryActionName, func(input agentrunaction.AgentMemoryInput) bool {
		return input.ConversationID == conversationID
	})
	require.Len(t, inputs, 1, "memory tasks")
	input := inputs[0]
	extractor := &scriptedMemoryExtractor{result: agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "report.md", Name: "周报格式", Description: "周报按客户分组", Body: "周报按客户分组整理。"},
	}}}
	extract := agentrunaction.NewExtractAgentMemoryAction(db, f.tasks, extractor, testModelInvoker(db))
	require.NoError(t, extract.Execute(ctx, input))
	require.Len(t, extractor.requests, 1, "extractions")
	recent := extractor.requests[0].Recent
	require.Empty(t, extractor.requests[0].Earlier)
	require.Contains(t, recent, agentruntime.MemoryMessage{Sender: "owner", Content: "以后周报按客户分组"})
	require.Equal(t, agentruntime.MemoryMessage{Sender: "assistant", Content: "好的，以后按客户分组"}, recent[len(recent)-1])
	// 没有新消息时不再调用模型。
	require.NoError(t, extract.Execute(ctx, input))
	require.Len(t, extractor.requests, 1, "repeat extraction calls")

	memories, err := agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	require.Equal(t, "周报格式", memories[0].Name)
	other := newChatLockUser(t, db, identity)
	_, err = agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, other, f.personalAgent.ID)
	require.ErrorIs(t, err, agentaction.ErrPersonalAgentNotFound, "other member memories")

	// 第二轮只分析新消息；提取期间负责人修改了记忆，本次结果作废，重试时基于负责人的版本提取，负责人的修改保留。
	second := f.sendAndLoadRun(conversationID, "我下周去上海出差")
	f.execute(memoryRuntime(t, 1, "记下了"), second.ID)
	extractor.during = func() {
		_, err := agentaction.NewUpdateAgentMemoryAction(db).Execute(ctx, identity, f.personalAgent.ID, memories[0].ID, agentaction.AgentMemoryInput{
			Name: "周报要求", Description: "周报按客户分组并附风险", Body: "周报按客户分组，每组附风险。",
		})
		assert.NoError(t, err)
	}
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "report.md", Name: "周报格式", Description: "旧版本", Body: "旧版本"},
	}}
	require.Error(t, extract.Execute(ctx, input), "extraction committed over owner edit")
	extractor.during = nil
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "travel.md", Name: "出差安排", Description: "负责人下周去上海", Body: "负责人下周去上海出差。"},
	}}
	require.NoError(t, extract.Execute(ctx, input))
	request := extractor.requests[2]
	require.Len(t, request.Recent, 2)
	require.Equal(t, "我下周去上海出差", request.Recent[0].Content)
	require.NotEmpty(t, request.Earlier)
	require.Len(t, request.Entries, 1)
	require.Equal(t, "周报要求", request.Entries[0].Name)
	memories, err = agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID)
	require.NoError(t, err)
	require.Len(t, memories, 2)
	names := []string{memories[0].Name, memories[1].Name}
	require.Contains(t, names, "周报要求")
	require.Contains(t, names, "出差安排")

	// 提取期间进度被其他任务推进时本次结果作废并报错重试，重试从新的进度继续。
	overlap := f.sendAndLoadRun(conversationID, "周报改成按项目分组")
	f.execute(memoryRuntime(t, 2, "好的，改成按项目分组"), overlap.ID)
	var progress int64
	require.NoError(t, db.NewSelect().Model((*servermodels.AgentConversation)(nil)).Column("memory_extracted_seq").
		Where("conversation_id = ?", conversationID).Scan(ctx, &progress))
	// 把进度设为 value，模拟其他提取任务已提交。
	setProgress := func(value int64) {
		_, err := db.NewUpdate().Model((*servermodels.AgentConversation)(nil)).Set("memory_extracted_seq = ?", value).
			Where("conversation_id = ?", conversationID).Exec(ctx)
		assert.NoError(t, err)
	}
	extractor.during = func() { setProgress(progress + 1) }
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "stale.md", Name: "过期结果", Description: "不应写入", Body: "不应写入"},
	}}
	require.Error(t, extract.Execute(ctx, input), "overlapping extraction committed")
	listed, err := agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2, "memories after overlapping extraction")
	extractor.during = nil
	setProgress(progress)
	extractor.result = agentruntime.MemoryExtractionResult{}
	require.NoError(t, extract.Execute(ctx, input))
	last := extractor.requests[len(extractor.requests)-1]
	require.Equal(t, "周报改成按项目分组", last.Recent[0].Content, "retried extraction=%+v", last)

	// 新运行读到当前记忆；负责人删除后不再出现。
	third := f.sendAndLoadRun(conversationID, "安排行程")
	f.execute(memoryRuntime(t, 2, "好的"), third.ID)
	for _, memory := range memories {
		require.NoError(t, agentaction.NewDeleteAgentMemoryAction(db).Execute(ctx, identity, f.personalAgent.ID, memory.ID))
	}
	fourth := f.sendAndLoadRun(conversationID, "再安排一次")
	f.execute(memoryRuntime(t, 0, "好的"), fourth.ID)
}

// memoryRuntime 返回核对本次运行启用记忆并读到指定条数记忆后以指定正文结束的运行时。
func memoryRuntime(t *testing.T, entries int, content string) testAgentRuntime {
	return testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if !assert.True(t, request.Assignment.Memory && request.Memory != nil, "personalAgent chat memory=%v loader=%v", request.Assignment.Memory, request.Memory != nil) {
			return completeTestRun(ctx, feed, content)
		}
		loaded, err := request.Memory(ctx)
		assert.NoError(t, err)
		assert.Len(t, loaded, entries, "run memory")
		return completeTestRun(ctx, feed, content)
	}}
}
