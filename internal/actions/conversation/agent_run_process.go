//go:build server

package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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

// Execute 校验运行所属会话的阅读资格后返回过程内容、任务清单和模型用量，成功、失败和取消的运行一律按已持久化的内容返回。
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
				domain.AgentRunStatusSucceeded, domain.AgentRunStatusFailed, domain.AgentRunStatusCancelled,
			})).
			Where("agr.started_at IS NOT NULL AND agr.completed_at IS NOT NULL").Scan(ctx)
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
		process = AgentRunProcess{ID: run.ID, DurationMilliseconds: run.CompletedAt.Sub(*run.StartedAt).Milliseconds(), Blocks: []agentruntime.Block{},
			Outcome: (*domain.AgentRunOutcome)(run.Outcome), OutcomeReason: (*domain.AgentHandoffReason)(run.OutcomeReason)}
		if err := json.Unmarshal(run.Usage, &process.Usage); err != nil {
			return fmt.Errorf("decode agent usage: %w", err)
		}
		if len(run.Plan) > 0 {
			if err := json.Unmarshal(run.Plan, &process.Plan); err != nil {
				return fmt.Errorf("decode agent run plan: %w", err)
			}
		}
		var blocks []servermodels.AgentRunBlock
		if err := tx.NewSelect().Model(&blocks).
			Where("arb.organization_id = ? AND arb.agent_run_id = ?", identity.Organization.ID, runID).
			OrderExpr("arb.position").Scan(ctx); err != nil {
			return fmt.Errorf("load agent process blocks: %w", err)
		}
		for _, block := range blocks {
			var payload agentruntime.BlockPayload
			if err := json.Unmarshal(block.Payload, &payload); err != nil {
				return fmt.Errorf("decode agent process block: %w", err)
			}
			process.Blocks = append(process.Blocks, agentruntime.Block{ID: block.ID, Position: block.Position, ModelCallID: block.ModelCallID, Kind: domain.AgentRunBlockKind(block.Kind), Payload: payload})
		}
		return nil
	})
	if err != nil {
		return AgentRunProcess{}, fmt.Errorf("read agent run process: %w", err)
	}
	return process, nil
}
