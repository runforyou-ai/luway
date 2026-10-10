//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/journaltest"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestAgentRunJournalContract 验证运行日志的数据库实现满足 einorun 的日志契约：修订号合并、宿主先交出、外部写入权威、提交与拒绝、步骤原子写入与恢复读取。
func TestAgentRunJournalContract(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "日志契约助手",
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答问题"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	journaltest.RunJournal(t, func(t *testing.T) journaltest.JournalHarness {
		// 每个用例使用一个处于执行中的新运行。
		first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity,
			directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "契约输入"})
		require.NoError(t, err)
		run := &servermodels.AgentRun{}
		require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx))
		_, err = db.NewUpdate().Model(run).Set("status = ?", domain.AgentRunStatusRunning).WherePK().Exec(ctx)
		require.NoError(t, err)
		journal, err := agentrunaction.NewRunJournal(ctx, db, tasks, run)
		require.NoError(t, err)
		return journaltest.JournalHarness{
			Journal: journal,
			Load: func(ctx context.Context) (einorun.Resume, error) {
				resume, err := agentrunaction.LoadResume(ctx, db, run)
				if err != nil {
					return einorun.Resume{}, err
				}
				if resume != nil {
					return *resume, nil
				}
				// 尚未保存恢复状态时只交回已写入的过程。
				blocks, calls, err := agentprocess.LoadRecords(ctx, db, run.WorkspaceID, run.ID)
				return einorun.Resume{Blocks: blocks, Calls: calls}, err
			},
			External: func(ctx context.Context, update einorun.ToolCall) error {
				stored := &servermodels.AgentToolCall{}
				if err := db.NewSelect().Model(stored).Where("atc.id = ?", update.ID).Scan(ctx); err != nil {
					return err
				}
				merged := einorun.OverlayExternal(agentprocess.CallRecord(*stored), update)
				media, _ := json.Marshal(merged.Media)
				query := db.NewUpdate().Model(stored).
					Set("status = ?", merged.Status).Set("result = ?", merged.Result).Set("error = ?", merged.Error).
					Set("completed_at = ?", merged.CompletedAt).Set("handover = ?", nilIfEmpty(string(merged.Handover))).
					Set("payload = ?::jsonb", nilIfEmpty(string(merged.Payload)))
				if merged.Media != nil {
					query = query.Set("media = ?::jsonb", string(media))
				}
				if merged.Decision != nil {
					decision, _ := json.Marshal(merged.Decision)
					query = query.Set("decision = ?::jsonb", string(decision))
				}
				_, err := query.WherePK().Exec(ctx)
				return err
			},
			Call: func(ctx context.Context, id string) (einorun.ToolCall, bool, error) {
				stored := &servermodels.AgentToolCall{}
				count, err := db.NewSelect().Model(stored).Where("atc.id = ?", id).ScanAndCount(ctx)
				if err != nil || count == 0 {
					return einorun.ToolCall{}, false, nil
				}
				return agentprocess.CallRecord(*stored), true, nil
			},
			Usage: func(ctx context.Context) (einorun.Usage, error) {
				saved := &servermodels.AgentRun{}
				if err := db.NewSelect().Model(saved).Column("usage").Where("id = ?", run.ID).Scan(ctx); err != nil {
					return einorun.Usage{}, err
				}
				var usage agentcontract.Usage
				if err := json.Unmarshal(saved.Usage, &usage); err != nil {
					return einorun.Usage{}, err
				}
				return einorun.Usage{Input: usage.PromptTokens, Output: usage.CompletionTokens, Total: usage.TotalTokens}, nil
			},
		}
	})
}

// nilIfEmpty 把空字符串转换为数据库空值。
func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
