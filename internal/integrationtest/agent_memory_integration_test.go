//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
func testAgentMemory(t *testing.T, f *deviceRunFixture) {
	ctx, db, identity := f.ctx, f.db, f.identity
	conversationID := f.personalAgentChat()
	run := f.sendAndLoadRun(conversationID, "以后周报按客户分组")
	claim, err := f.executor.ClaimDeviceRun(ctx, f.device, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var assignment agentruntime.Assignment
	if err := json.Unmarshal(claim.Assignment, &assignment); err != nil || !assignment.Memory {
		t.Fatalf("personalAgent chat assignment memory=%v err=%v", assignment.Memory, err)
	}
	if entries, err := f.executor.LoadDeviceRunMemory(ctx, f.device, run.ID); err != nil || len(entries) != 0 {
		t.Fatalf("initial memory=%+v err=%v", entries, err)
	}
	f.complete(run.ID, "好的，以后按客户分组")

	var tasks []servermodels.TaskRun
	if err := db.NewSelect().Model(&tasks).
		Where("tr.action_name = ? AND tr.payload->>'conversationId' = ?", agentrunaction.AgentMemoryActionName, conversationID).
		Scan(ctx); err != nil || len(tasks) != 1 {
		t.Fatalf("memory tasks=%d err=%v", len(tasks), err)
	}
	var input agentrunaction.AgentMemoryInput
	if err := json.Unmarshal(tasks[0].Payload, &input); err != nil {
		t.Fatal(err)
	}
	extractor := &scriptedMemoryExtractor{result: agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "report.md", Name: "周报格式", Description: "周报按客户分组", Body: "周报按客户分组整理。"},
	}}}
	extract := agentrunaction.NewExtractAgentMemoryAction(db, f.tasks, extractor, testModelInvoker(db))
	if err := extract.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if len(extractor.requests) != 1 {
		t.Fatalf("extractions=%d", len(extractor.requests))
	}
	recent := extractor.requests[0].Recent
	if len(extractor.requests[0].Earlier) != 0 || !slices.Contains(recent, agentruntime.MemoryMessage{Sender: "owner", Content: "以后周报按客户分组"}) ||
		recent[len(recent)-1] != (agentruntime.MemoryMessage{Sender: "assistant", Content: "好的，以后按客户分组"}) {
		t.Fatalf("first extraction=%+v", extractor.requests[0])
	}
	// 没有新消息时不再调用模型。
	if err := extract.Execute(ctx, input); err != nil || len(extractor.requests) != 1 {
		t.Fatalf("repeat extraction calls=%d err=%v", len(extractor.requests), err)
	}

	memories, err := agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID)
	if err != nil || len(memories) != 1 || memories[0].Name != "周报格式" {
		t.Fatalf("memories=%+v err=%v", memories, err)
	}
	other := newChatLockUser(t, db, identity)
	if _, err := agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, other, f.personalAgent.ID); !errors.Is(err, agentaction.ErrPersonalAgentNotFound) {
		t.Fatalf("other member memories=%v", err)
	}
	if _, err := agentaction.NewUpdateAgentMemoryAction(db).Execute(ctx, identity, f.personalAgent.ID, memories[0].ID, agentaction.AgentMemoryInput{Name: "", Description: "x", Body: "x"}); err == nil {
		t.Fatal("empty memory name accepted")
	}

	// 第二轮只分析新消息；提取期间负责人修改了记忆，本次结果作废，重试时基于负责人的版本提取，负责人的修改保留。
	second := f.sendAndLoadRun(conversationID, "我下周去上海出差")
	f.claimAndComplete(second.ID, "记下了")
	extractor.during = func() {
		if _, err := agentaction.NewUpdateAgentMemoryAction(db).Execute(ctx, identity, f.personalAgent.ID, memories[0].ID, agentaction.AgentMemoryInput{
			Name: "周报要求", Description: "周报按客户分组并附风险", Body: "周报按客户分组，每组附风险。",
		}); err != nil {
			t.Error(err)
		}
	}
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "report.md", Name: "周报格式", Description: "旧版本", Body: "旧版本"},
	}}
	if err := extract.Execute(ctx, input); err == nil {
		t.Fatal("extraction committed over owner edit")
	}
	extractor.during = nil
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "travel.md", Name: "出差安排", Description: "负责人下周去上海", Body: "负责人下周去上海出差。"},
	}}
	if err := extract.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	request := extractor.requests[2]
	if len(request.Recent) != 2 || request.Recent[0].Content != "我下周去上海出差" || len(request.Earlier) == 0 ||
		len(request.Entries) != 1 || request.Entries[0].Name != "周报要求" {
		t.Fatalf("retried second extraction=%+v", request)
	}
	memories, err = agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID)
	if err != nil || len(memories) != 2 {
		t.Fatalf("memories after second extraction=%+v err=%v", memories, err)
	}
	names := []string{memories[0].Name, memories[1].Name}
	if !slices.Contains(names, "周报要求") || !slices.Contains(names, "出差安排") {
		t.Fatalf("memory names=%v", names)
	}

	// 提取期间进度被其他任务推进时本次结果作废并报错重试，重试从新的进度继续。
	overlap := f.sendAndLoadRun(conversationID, "周报改成按项目分组")
	f.claimAndComplete(overlap.ID, "好的，改成按项目分组")
	var progress int64
	if err := db.NewSelect().Model((*servermodels.AgentConversation)(nil)).Column("memory_extracted_seq").
		Where("conversation_id = ?", conversationID).Scan(ctx, &progress); err != nil {
		t.Fatal(err)
	}
	// 把进度设为 value，模拟其他提取任务已提交。
	setProgress := func(value int64) {
		if _, err := db.NewUpdate().Model((*servermodels.AgentConversation)(nil)).Set("memory_extracted_seq = ?", value).
			Where("conversation_id = ?", conversationID).Exec(ctx); err != nil {
			t.Error(err)
		}
	}
	extractor.during = func() { setProgress(progress + 1) }
	extractor.result = agentruntime.MemoryExtractionResult{Saved: []agentruntime.MemoryEntry{
		{Path: "stale.md", Name: "过期结果", Description: "不应写入", Body: "不应写入"},
	}}
	if err := extract.Execute(ctx, input); err == nil {
		t.Fatal("overlapping extraction committed")
	}
	if listed, err := agentaction.NewListAgentMemoriesQuery(db).Execute(ctx, identity, f.personalAgent.ID); err != nil || len(listed) != 2 {
		t.Fatalf("memories after overlapping extraction=%+v err=%v", listed, err)
	}
	extractor.during = nil
	setProgress(progress)
	extractor.result = agentruntime.MemoryExtractionResult{}
	if err := extract.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if last := extractor.requests[len(extractor.requests)-1]; last.Recent[0].Content != "周报改成按项目分组" {
		t.Fatalf("retried extraction=%+v", last)
	}

	// 新运行读到当前记忆；负责人删除后不再出现。
	third := f.sendAndLoadRun(conversationID, "安排行程")
	if _, err := f.executor.ClaimDeviceRun(ctx, f.device, third.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := f.executor.LoadDeviceRunMemory(ctx, f.device, third.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("run memory=%+v err=%v", entries, err)
	}
	for _, memory := range memories {
		if err := agentaction.NewDeleteAgentMemoryAction(db).Execute(ctx, identity, f.personalAgent.ID, memory.ID); err != nil {
			t.Fatal(err)
		}
	}
	if entries, err := f.executor.LoadDeviceRunMemory(ctx, f.device, third.ID); err != nil || len(entries) != 0 {
		t.Fatalf("memory after delete=%+v err=%v", entries, err)
	}
	f.complete(third.ID, "好的")
}
