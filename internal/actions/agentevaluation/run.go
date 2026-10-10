//go:build server

package agentevaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// EvaluateActionName 是执行一次评测尝试的任务名称。
const EvaluateActionName = "agent_evaluation.evaluate"

var (
	// ErrRunInProgress 表示 AI 员工已有进行中的评测运行。
	ErrRunInProgress = errors.New("agent evaluation run in progress")
	// ErrNoCases 表示 AI 员工还没有评测用例。
	ErrNoCases = errors.New("agent has no evaluation cases")
	// ErrDecisionModelUnavailable 表示工作区未设置判断模型或所设模型已不存在。
	ErrDecisionModelUnavailable = errors.New("decision model unavailable")
	// ErrConfigurationUnavailable 表示 AI 员工当前生效的配置不是托管对话模型配置。
	ErrConfigurationUnavailable = errors.New("agent configuration unavailable for evaluation")
	// ErrResultNotFound 表示最近一次运行中不存在指定用例的结果。
	ErrResultNotFound = errors.New("evaluation result not found")
	// ErrCaseChanged 表示用例在最近一次运行后已修改，重跑无法验证当前内容。
	ErrCaseChanged = errors.New("evaluation case changed since latest run")
)

// EvaluateInput 定义评测任务输入。
type EvaluateInput struct {
	WorkspaceID string `json:"workspaceId"`
	ResultID    string `json:"resultId"`
}

// CaseSnapshot 是运行发起时冻结的用例内容，回放、判定与展示都以它为准；来源周期与提问时间用于限定客户历史检索，手动用例为空。
type CaseSnapshot struct {
	Audience         domain.ServiceAudience `json:"audience"`
	ServiceSessionID *string                `json:"serviceSessionId,omitempty"`
	OccurredAt       *time.Time             `json:"occurredAt,omitempty"`
	Context          CaseContext            `json:"context"`
	Question         string                 `json:"question"`
	ExpectedAction   domain.AgentRunOutcome `json:"expectedAction"`
	ExpectedAnswer   string                 `json:"expectedAnswer"`
}

// StartRunAction 用 AI 员工当前生效的配置发起一次评测运行。
type StartRunAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewStartRunAction 创建发起评测运行操作。
func NewStartRunAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *StartRunAction {
	return &StartRunAction{db: db, enqueuer: enqueuer}
}

// Execute 在同一事务中冻结全部用例快照、写入首次尝试并逐条投递评测任务，返回运行编号；已有进行中的运行、没有用例、未设置判断模型或当前配置不是托管对话模型时拒绝。
func (a *StartRunAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string) (string, error) {
	var runID string
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		agent, err := lockEvaluatedAgent(ctx, tx, identity.Workspace.ID, agentID)
		if err != nil {
			return err
		}
		if err := ensureEvaluable(ctx, tx, agent); err != nil {
			return err
		}
		cases := make([]servermodels.AgentEvaluationCase, 0)
		if err := tx.NewSelect().Model(&cases).
			Where("aec.workspace_id = ? AND aec.agent_id = ?", agent.WorkspaceID, agent.ID).
			OrderExpr("aec.created_at, aec.id").Scan(ctx); err != nil {
			return fmt.Errorf("load evaluation cases: %w", err)
		}
		if len(cases) == 0 {
			return ErrNoCases
		}
		run := &servermodels.AgentEvaluationRun{
			WorkspaceID: agent.WorkspaceID, AgentID: agent.ID, AgentRevisionID: agent.ActiveRevisionID,
			Status: string(domain.AgentEvaluationRunStatusRunning), CaseCount: len(cases), StartedByIdentityID: identity.WorkspaceIdentity.ID,
		}
		if _, err := tx.NewInsert().Model(run).
			Column("workspace_id", "agent_id", "agent_revision_id", "status", "case_count", "started_by_identity_id").
			Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("create evaluation run: %w", err)
		}
		runID = run.ID
		for _, record := range cases {
			evaluationCase, err := caseFromRecord(&record)
			if err != nil {
				return err
			}
			snapshot, err := json.Marshal(CaseSnapshot{
				Audience: evaluationCase.Audience, ServiceSessionID: evaluationCase.ServiceSessionID, OccurredAt: evaluationCase.OccurredAt,
				Context: evaluationCase.Context, Question: evaluationCase.Question,
				ExpectedAction: evaluationCase.ExpectedAction, ExpectedAnswer: evaluationCase.ExpectedAnswer,
			})
			if err != nil {
				return err
			}
			if err := insertAttempt(ctx, tx, a.enqueuer, run, record.ID, 1, record.Version, snapshot); err != nil {
				return err
			}
		}
		return nil
	})
	return runID, err
}

// RerunCaseAction 在最近一次运行中重新运行一条用例。
type RerunCaseAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewRerunCaseAction 创建单条重跑操作。
func NewRerunCaseAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *RerunCaseAction {
	return &RerunCaseAction{db: db, enqueuer: enqueuer}
}

// Execute 用最近一次运行的配置版本和该用例的冻结快照新增一次尝试，用于确认结果是否由模型输出波动造成；有进行中的运行、用例已删除或已在该运行后修改时拒绝，新尝试不改变运行计数。
func (a *RerunCaseAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, caseID string) error {
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		agent, err := lockEvaluatedAgent(ctx, tx, identity.Workspace.ID, agentID)
		if err != nil {
			return err
		}
		if err := ensureDecisionModel(ctx, tx, agent.WorkspaceID); err != nil {
			return err
		}
		run := &servermodels.AgentEvaluationRun{}
		err = tx.NewSelect().Model(run).
			Where("aer.workspace_id = ? AND aer.agent_id = ?", agent.WorkspaceID, agent.ID).
			OrderExpr("aer.created_at DESC, aer.id DESC").Limit(1).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrResultNotFound
		}
		if err != nil {
			return fmt.Errorf("load latest evaluation run: %w", err)
		}
		if domain.AgentEvaluationRunStatus(run.Status) == domain.AgentEvaluationRunStatusRunning {
			return ErrRunInProgress
		}
		record, err := lockCase(ctx, tx, agent.WorkspaceID, agent.ID, caseID)
		if err != nil {
			return err
		}
		latest := &servermodels.AgentEvaluationResult{}
		err = tx.NewSelect().Model(latest).
			Where("aers.workspace_id = ? AND aers.run_id = ? AND aers.case_id = ?", run.WorkspaceID, run.ID, caseID).
			OrderExpr("aers.attempt DESC").Limit(1).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrResultNotFound
		}
		if err != nil {
			return fmt.Errorf("load evaluation result: %w", err)
		}
		if domain.AgentEvaluationResultStatus(latest.Status) == domain.AgentEvaluationResultStatusPending {
			return ErrRunInProgress
		}
		if latest.CaseVersion != record.Version {
			return ErrCaseChanged
		}
		return insertAttempt(ctx, tx, a.enqueuer, run, caseID, latest.Attempt+1, latest.CaseVersion, latest.CaseSnapshot)
	})
}

// insertAttempt 写入一次待执行的尝试并在同一事务中投递评测任务。
func insertAttempt(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, run *servermodels.AgentEvaluationRun, caseID string, attempt, caseVersion int, snapshot json.RawMessage) error {
	result := &servermodels.AgentEvaluationResult{
		WorkspaceID: run.WorkspaceID, RunID: run.ID, CaseID: caseID, Attempt: attempt, CaseVersion: caseVersion,
		CaseSnapshot: snapshot, Status: string(domain.AgentEvaluationResultStatusPending),
	}
	if _, err := tx.NewInsert().Model(result).
		Column("workspace_id", "run_id", "case_id", "attempt", "case_version", "case_snapshot", "status").
		Returning("id").Exec(ctx); err != nil {
		return fmt.Errorf("create evaluation result: %w", err)
	}
	if _, err := enqueuer.EnqueueIn(ctx, EvaluateActionName, EvaluateInput{WorkspaceID: run.WorkspaceID, ResultID: result.ID}, servertask.EnqueueOptions{
		WorkspaceID: run.WorkspaceID, Queue: servertask.QueueEvaluation, IdempotencyKey: "agent-evaluation:" + result.ID,
	}); err != nil {
		return fmt.Errorf("enqueue evaluation: %w", err)
	}
	return nil
}

// ensureEvaluable 校验 AI 员工没有进行中的运行、工作区已设置判断模型，且当前生效配置为托管执行。
func ensureEvaluable(ctx context.Context, tx bun.Tx, agent *servermodels.Agent) error {
	running, err := tx.NewSelect().Model((*servermodels.AgentEvaluationRun)(nil)).
		Where("aer.workspace_id = ? AND aer.agent_id = ? AND aer.status = ?", agent.WorkspaceID, agent.ID, domain.AgentEvaluationRunStatusRunning).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check running evaluation: %w", err)
	}
	if running {
		return ErrRunInProgress
	}
	if err := ensureDecisionModel(ctx, tx, agent.WorkspaceID); err != nil {
		return err
	}
	managed, err := tx.NewSelect().TableExpr("agent_revisions AS ar").
		Where("ar.workspace_id = ? AND ar.id = ? AND ar.execution_mode = ?", agent.WorkspaceID, agent.ActiveRevisionID, domain.AgentExecutionModeManaged).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check evaluated agent execution: %w", err)
	}
	if !managed {
		return ErrConfigurationUnavailable
	}
	return nil
}

// ensureDecisionModel 校验工作区已设置判断模型且模型仍存在。
func ensureDecisionModel(ctx context.Context, db bun.IDB, workspaceID string) error {
	model, err := loadDecisionModel(ctx, db, workspaceID)
	if err != nil {
		return err
	}
	if model == nil {
		return ErrDecisionModelUnavailable
	}
	return nil
}

// loadDecisionModel 读取工作区的判断模型，未设置或模型已不存在时返回 nil。
func loadDecisionModel(ctx context.Context, db bun.IDB, workspaceID string) (*aimodel.Model, error) {
	settings, err := customerservice.LoadServiceSummarySettings(ctx, db, workspaceID)
	if err != nil {
		return nil, err
	}
	return customerservice.LoadModel(ctx, db, workspaceID, settings.DecisionModelID, domain.AIModelUsageDecision)
}
