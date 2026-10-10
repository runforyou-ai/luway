//go:build server

// Package processquery 读取成员可阅读会话中 Agent 运行的过程：消息窗口内的运行状态与过程引用、单次运行的过程内容、运行过程流授权与电脑执行的工具调用过程更新。
package processquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrRunProcessUnavailable 表示运行过程不存在或当前身份无权读取。
	ErrRunProcessUnavailable = errors.New("agent run process unavailable")
	// ErrToolCallProcessUnavailable 表示工具调用不存在、不是电脑执行的调用或当前身份无权读取其所属会话。
	ErrToolCallProcessUnavailable = errors.New("agent tool call process unavailable")
)

// RunProcess 是一次已完成运行的有序过程内容、任务清单和模型用量。
type RunProcess struct {
	ID                   string
	DurationMilliseconds int64
	Usage                agentcontract.Usage
	Outcome              *domain.AgentRunOutcome
	OutcomeReason        *domain.AgentHandoffReason
	Blocks               []agentcontract.Block
	Plan                 []stream.PlanTask
}

// GetRunProcessQuery 按运行编号读取一次已完成运行的有序过程内容。
type GetRunProcessQuery struct {
	db *bun.DB
}

// NewGetRunProcessQuery 创建运行过程详情查询。
func NewGetRunProcessQuery(db *bun.DB) *GetRunProcessQuery {
	return &GetRunProcessQuery{db: db}
}

// Execute 校验运行所属会话的阅读资格后返回过程内容、任务清单和模型用量，已结束和挂起等待的运行一律按已持久化的内容返回。
func (q *GetRunProcessQuery) Execute(ctx context.Context, identity *servermodels.Identity, runID string) (RunProcess, error) {
	if !str.IsUUID(runID) {
		return RunProcess{}, ErrRunProcessUnavailable
	}
	var process RunProcess
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var run servermodels.AgentRun
		err := tx.NewSelect().Model(&run).
			Where("agr.workspace_id = ? AND agr.id = ?", identity.Workspace.ID, runID).
			Where("agr.status IN (?)", bun.List([]domain.AgentRunStatus{
				domain.AgentRunStatusSucceeded, domain.AgentRunStatusFailed, domain.AgentRunStatusCancelled, domain.AgentRunStatusWaiting,
			})).
			Where("agr.started_at IS NOT NULL AND (agr.completed_at IS NOT NULL OR agr.status = ?)", domain.AgentRunStatusWaiting).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRunProcessUnavailable
		} else if err != nil {
			return fmt.Errorf("load agent run: %w", err)
		}
		// 会话不可读与运行不存在对外是同一结果，不暴露运行是否存在。
		if err := conversationaccess.RequireReadable(ctx, tx, identity, run.ConversationID); err != nil {
			if errors.Is(err, chatstate.ErrConversationNotFound) {
				return ErrRunProcessUnavailable
			}
			return err
		}
		process = RunProcess{ID: run.ID, DurationMilliseconds: runDuration(&run).Milliseconds(),
			Outcome: (*domain.AgentRunOutcome)(run.Outcome), OutcomeReason: (*domain.AgentHandoffReason)(run.OutcomeReason)}
		if err := json.Unmarshal(run.Usage, &process.Usage); err != nil {
			return fmt.Errorf("decode agent usage: %w", err)
		}
		if len(run.Plan) > 0 {
			if err := json.Unmarshal(run.Plan, &process.Plan); err != nil {
				return fmt.Errorf("decode agent run plan: %w", err)
			}
		}
		blocks, _, err := agentprocess.Load(ctx, tx, identity.Workspace.ID, runID)
		if err != nil {
			return err
		}
		process.Blocks = blocks
		return nil
	})
	if err != nil {
		return RunProcess{}, fmt.Errorf("read agent run process: %w", err)
	}
	return process, nil
}
