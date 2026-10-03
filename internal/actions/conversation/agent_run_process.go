//go:build server

package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetAgentRunProcessQuery 按运行编号读取一次已完成运行的有序过程内容。
type GetAgentRunProcessQuery struct {
	db *bun.DB
}

// NewGetAgentRunProcessQuery 创建运行过程详情查询。
func NewGetAgentRunProcessQuery(db *bun.DB) *GetAgentRunProcessQuery {
	return &GetAgentRunProcessQuery{db: db}
}

// Execute 校验运行所属会话的阅读资格后返回过程内容、任务清单和模型用量，已结束和挂起等待的运行一律按已持久化的内容返回。
func (q *GetAgentRunProcessQuery) Execute(ctx context.Context, identity *servermodels.Identity, runID string) (AgentRunProcess, error) {
	if !common.ValidUUID(runID) {
		return AgentRunProcess{}, ErrAgentRunProcessUnavailable
	}
	var process AgentRunProcess
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var run servermodels.AgentRun
		err := tx.NewSelect().Model(&run).
			Where("agr.organization_id = ? AND agr.id = ?", identity.Organization.ID, runID).
			Where("agr.status IN (?)", bun.In([]domain.AgentRunStatus{
				domain.AgentRunStatusSucceeded, domain.AgentRunStatusFailed, domain.AgentRunStatusCancelled, domain.AgentRunStatusWaiting,
			})).
			Where("agr.started_at IS NOT NULL AND (agr.completed_at IS NOT NULL OR agr.status = ?)", domain.AgentRunStatusWaiting).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAgentRunProcessUnavailable
		} else if err != nil {
			return fmt.Errorf("load agent run: %w", err)
		}
		// 会话不可读与运行不存在对外是同一结果，不暴露运行是否存在。
		if err := AuthorizeConversationHistory(ctx, tx, identity, run.ConversationID); err != nil {
			if errors.Is(err, ErrConversationNotFound) {
				return ErrAgentRunProcessUnavailable
			}
			return err
		}
		process = AgentRunProcess{ID: run.ID, DurationMilliseconds: runDuration(&run).Milliseconds(),
			Outcome: (*domain.AgentRunOutcome)(run.Outcome), OutcomeReason: (*domain.AgentHandoffReason)(run.OutcomeReason)}
		if err := json.Unmarshal(run.Usage, &process.Usage); err != nil {
			return fmt.Errorf("decode agent usage: %w", err)
		}
		if len(run.Plan) > 0 {
			if err := json.Unmarshal(run.Plan, &process.Plan); err != nil {
				return fmt.Errorf("decode agent run plan: %w", err)
			}
		}
		blocks, _, err := agentprocess.Load(ctx, tx, identity.Organization.ID, runID)
		if err != nil {
			return err
		}
		process.Blocks = blocks
		return nil
	})
	if err != nil {
		return AgentRunProcess{}, fmt.Errorf("read agent run process: %w", err)
	}
	return process, nil
}
