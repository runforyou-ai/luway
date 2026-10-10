//go:build server

package agentevaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// correctThreshold 是判断模型认定答案符合标准答案的最低概率，与质检阈值一致。
	correctThreshold = 0.8
	// toolResultMaxRunes 限制交给判断模型的单个工具返回长度。
	toolResultMaxRunes = 4000
	// toolResultWindowPercent 是工具返回与前文合计可占判断模型上下文窗口的百分比。
	toolResultWindowPercent = 50
)

// Replayer 用指定配置版本离线处理一次服务场景输入。
type Replayer interface {
	Replay(context.Context, agentrunaction.ReplayInput) (agentruntime.RunResult, error)
}

// Worker 执行评测任务：回放用例、判定并写入结果。
type Worker struct {
	db       *bun.DB
	replayer Replayer
	invoker  *modelcall.Invoker
}

// NewWorker 创建评测任务执行器，判断模型经统一调用入口调用。
func NewWorker(db *bun.DB, replayer Replayer, invoker *modelcall.Invoker) *Worker {
	return &Worker{db: db, replayer: replayer, invoker: invoker}
}

// outcome 是一次尝试的判定结果。
type outcome struct {
	status             domain.AgentEvaluationResultStatus
	actualAction       *string
	actualReason       *string
	answer             string
	blocks             []agentcontract.Block
	usage              agentcontract.Usage
	correctProbability *float64
	errorCode          *domain.AgentEvaluationErrorCode
}

// Evaluate 回放一次尝试的用例快照并判定：处理方式与期望不一致即未通过；期望答复、追问或结束会话时由判断模型检查答案；模型调用失败或超时记为评测异常。尝试已结束时不重复执行。
func (w *Worker) Evaluate(ctx context.Context, input EvaluateInput) error {
	pending := struct {
		servermodels.AgentEvaluationResult `bun:",embed"`
		AgentID                            string `bun:"agent_id"`
		AgentRevisionID                    string `bun:"agent_revision_id"`
	}{}
	err := w.db.NewSelect().TableExpr("agent_evaluation_results AS aers").
		ColumnExpr("aers.*, aer.agent_id, aer.agent_revision_id").
		Join("JOIN agent_evaluation_runs AS aer ON aer.id = aers.run_id AND aer.workspace_id = aers.workspace_id").
		Where("aers.workspace_id = ? AND aers.id = ?", input.WorkspaceID, input.ResultID).
		Scan(ctx, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load evaluation result: %w", err)
	}
	if domain.AgentEvaluationResultStatus(pending.Status) != domain.AgentEvaluationResultStatusPending {
		return nil
	}
	snapshot := CaseSnapshot{}
	if err := json.Unmarshal(pending.CaseSnapshot, &snapshot); err != nil {
		return fmt.Errorf("decode evaluation case snapshot: %w", err)
	}
	replay := agentrunaction.ReplayInput{
		ReplayID: pending.ID, WorkspaceID: input.WorkspaceID, AgentID: pending.AgentID, RevisionID: pending.AgentRevisionID,
		Audience: snapshot.Audience, Messages: replayMessages(pending.ID, snapshot),
	}
	if customer := snapshot.Context.Customer; customer != nil {
		replay.Customer = &agentrunaction.ServiceSessionCustomer{UserID: customer.UserID, Email: customer.Email}
	}
	if member := snapshot.Context.Member; member != nil {
		replay.Member = &agentrunaction.ServiceMember{UserID: member.UserID, Email: member.Email}
	}
	if snapshot.Context.CustomerHistory && snapshot.ServiceSessionID != nil && snapshot.OccurredAt != nil {
		replay.History = &agentrunaction.ReplayHistory{ServiceSessionID: *snapshot.ServiceSessionID, ClosedBefore: *snapshot.OccurredAt}
	}
	result, runErr := w.replayer.Replay(ctx, replay)
	if runErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	judged := outcome{blocks: agentprocess.BlockViews(result.Blocks), usage: result.Usage}
	switch {
	case errors.Is(runErr, agentrunaction.ErrReplayConfigurationUnavailable):
		judged.status, judged.errorCode = domain.AgentEvaluationResultStatusError, new(domain.AgentEvaluationErrorConfigurationUnavailable)
	case errors.Is(runErr, context.DeadlineExceeded):
		judged.status, judged.errorCode = domain.AgentEvaluationResultStatusError, new(domain.AgentEvaluationErrorTimeout)
	case runErr != nil:
		slog.WarnContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "评测回放失败", "evaluation_result_id", pending.ID, "error", runErr)
		judged.status, judged.errorCode = domain.AgentEvaluationResultStatusError, new(domain.AgentEvaluationErrorRuntimeFailed)
	default:
		if err := w.judge(ctx, input.WorkspaceID, pending.ID, snapshot, result, &judged); err != nil {
			return err
		}
	}
	return w.save(ctx, input.WorkspaceID, pending.ID, judged)
}

// judge 按处理方式与标准答案判定一次正常结束的回放。
func (w *Worker) judge(ctx context.Context, workspaceID, evaluationID string, snapshot CaseSnapshot, result agentruntime.RunResult, judged *outcome) error {
	actual := result.Decision.Outcome()
	judged.actualAction, judged.answer = new(string(actual)), result.Content
	if actual == domain.AgentRunOutcomeHandoff {
		judged.actualReason = new(string(result.Decision.Reason))
	}
	if actual != snapshot.ExpectedAction {
		judged.status = domain.AgentEvaluationResultStatusFailed
		return nil
	}
	if actual == domain.AgentRunOutcomeHandoff {
		judged.status = domain.AgentEvaluationResultStatusPassed
		return nil
	}
	model, err := loadDecisionModel(ctx, w.db, workspaceID)
	if err != nil {
		return err
	}
	if model == nil {
		judged.status, judged.errorCode = domain.AgentEvaluationResultStatusError, new(domain.AgentEvaluationErrorDecisionModelUnavailable)
		return nil
	}
	answers, err := w.invoker.Decide(ctx, modelcall.SystemScope(workspaceID, domain.AIModelCallSourceAgentEvaluation, evaluationID), model,
		judgeState(snapshot, result, model.ContextWindow), map[string]decision.Question{
			"correct": {Kind: decision.KindYesNo,
				Instructions: "AI 的回复覆盖了标准答案的要点、没有与之矛盾的内容，并且与本次知识库检索和业务查询返回的资料一致，没有编造资料中没有的事实。"},
		})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.WarnContext(logscope.WithWorkspace(ctx, workspaceID), "评测判定失败", "error", err)
		judged.status, judged.errorCode = domain.AgentEvaluationResultStatusError, new(domain.AgentEvaluationErrorDecisionFailed)
		return nil
	}
	probability := answers["correct"].Probability
	judged.correctProbability = &probability
	judged.status = domain.AgentEvaluationResultStatusFailed
	if probability >= correctThreshold {
		judged.status = domain.AgentEvaluationResultStatusPassed
	}
	return nil
}

// save 写入一次尝试的结果；首次尝试全部结束后按首次尝试的结果写入运行计数并结束运行。
func (w *Worker) save(ctx context.Context, workspaceID, resultID string, judged outcome) error {
	blocks, err := json.Marshal(judged.blocks)
	if err != nil {
		return err
	}
	if judged.blocks == nil {
		blocks = []byte("[]")
	}
	usage, err := json.Marshal(judged.usage)
	if err != nil {
		return err
	}
	return serverstorage.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		result := &servermodels.AgentEvaluationResult{}
		if err := tx.NewSelect().Model(result).
			Where("aers.workspace_id = ? AND aers.id = ?", workspaceID, resultID).
			For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock evaluation result: %w", err)
		}
		if domain.AgentEvaluationResultStatus(result.Status) != domain.AgentEvaluationResultStatusPending {
			return nil
		}
		// 先锁运行再写结果，同一运行的计数由持有运行锁的尝试串行重算。
		run := &servermodels.AgentEvaluationRun{}
		if err := tx.NewSelect().Model(run).
			Where("aer.workspace_id = ? AND aer.id = ?", workspaceID, result.RunID).
			For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock evaluation run: %w", err)
		}
		if _, err := tx.NewUpdate().Model(result).
			Set("status = ?", judged.status).
			Set("actual_action = ?", judged.actualAction).
			Set("actual_reason = ?", judged.actualReason).
			Set("answer = ?", judged.answer).
			Set("blocks = ?::jsonb", string(blocks)).
			Set("usage = ?::jsonb", string(usage)).
			Set("correct_probability = ?", judged.correctProbability).
			Set("error = ?", judged.errorCode).
			Set("completed_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("save evaluation result: %w", err)
		}
		if result.Attempt != 1 || domain.AgentEvaluationRunStatus(run.Status) != domain.AgentEvaluationRunStatusRunning {
			return nil
		}
		return completeRunIfFinished(ctx, tx, run)
	})
}

// FinalizeFailure 在评测任务重试耗尽后把仍未结束的尝试记为评测异常，使运行可以结束。
func (w *Worker) FinalizeFailure(ctx context.Context, input EvaluateInput, runErr error) error {
	slog.WarnContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "评测任务重试耗尽", "evaluation_result_id", input.ResultID, "error", runErr)
	return w.save(ctx, input.WorkspaceID, input.ResultID, outcome{status: domain.AgentEvaluationResultStatusError, errorCode: new(domain.AgentEvaluationErrorRuntimeFailed)})
}

// completeRunIfFinished 首次尝试全部结束时写入各状态计数并把运行置为已完成。
func completeRunIfFinished(ctx context.Context, tx bun.Tx, run *servermodels.AgentEvaluationRun) error {
	counts := struct {
		Pending int `bun:"pending"`
		Passed  int `bun:"passed"`
		Failed  int `bun:"failed"`
		Error   int `bun:"error"`
	}{}
	if err := tx.NewSelect().TableExpr("agent_evaluation_results AS aers").
		ColumnExpr("count(*) FILTER (WHERE aers.status = ?) AS pending", domain.AgentEvaluationResultStatusPending).
		ColumnExpr("count(*) FILTER (WHERE aers.status = ?) AS passed", domain.AgentEvaluationResultStatusPassed).
		ColumnExpr("count(*) FILTER (WHERE aers.status = ?) AS failed", domain.AgentEvaluationResultStatusFailed).
		ColumnExpr("count(*) FILTER (WHERE aers.status = ?) AS error", domain.AgentEvaluationResultStatusError).
		Where("aers.workspace_id = ? AND aers.run_id = ? AND aers.attempt = 1", run.WorkspaceID, run.ID).
		Scan(ctx, &counts); err != nil {
		return fmt.Errorf("count evaluation results: %w", err)
	}
	if counts.Pending > 0 {
		return nil
	}
	if _, err := tx.NewUpdate().Model(run).
		Set("status = ?", domain.AgentEvaluationRunStatusCompleted).
		Set("passed_count = ?", counts.Passed).
		Set("failed_count = ?", counts.Failed).
		Set("error_count = ?", counts.Error).
		Set("completed_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("complete evaluation run: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "评测运行已完成", "evaluation_run_id", run.ID,
		"passed", counts.Passed, "failed", counts.Failed, "error", counts.Error)
	return nil
}

// replayMessages 把用例快照投影为回放的模型上下文：客户上下文、前文与提问，提问人的消息为用户角色，AI 与真人处理人的消息为助手角色。
func replayMessages(replayID string, snapshot CaseSnapshot) []agentcontract.Message {
	messages := make([]agentcontract.Message, 0, len(snapshot.Context.Messages)+2)
	if snapshot.Context.CustomerContext != "" {
		messages = append(messages, agentcontract.Message{ID: "customer-context:" + replayID, Role: agentcontract.MessageRoleUser, Content: snapshot.Context.CustomerContext})
	}
	for index, message := range snapshot.Context.Messages {
		role := agentcontract.MessageRoleAssistant
		if message.Sender == contextSenderCustomer {
			role = agentcontract.MessageRoleUser
		}
		messages = append(messages, agentcontract.Message{ID: "context:" + replayID + ":" + strconv.Itoa(index), Role: role, Content: message.Body})
	}
	return append(messages, agentcontract.Message{ID: "question:" + replayID, Role: agentcontract.MessageRoleUser, Content: snapshot.Question})
}

// judgeState 返回判断模型的资料：前文、提问、标准答案、AI 回复与本次知识库检索和业务查询的返回内容；工具返回与前文共用判断模型窗口一半的预算，先保留工具返回，再保留前文，各自从新到旧取，单个工具返回另按字符数截断。
func judgeState(snapshot CaseSnapshot, result agentruntime.RunResult, contextWindow int64) map[string]any {
	type toolResult struct {
		Tool   string `json:"tool"`
		Result string `json:"result"`
	}
	budget := llm.ContextWindow(int(contextWindow)) * toolResultWindowPercent / 100
	results := make([]toolResult, 0)
	for index := len(result.Blocks) - 1; index >= 0; index-- {
		call := result.Blocks[index].Call
		if call == nil || call.Result == nil || call.Name == "ask_customer" || call.Name == "handoff_to_human" || call.Name == "resolve_conversation" {
			continue
		}
		content := str.Substr(*call.Result, 0, toolResultMaxRunes)
		cost := llm.EstimateTokens(content)
		if cost > budget {
			break
		}
		budget -= cost
		results = append(results, toolResult{Tool: call.Name, Result: content})
	}
	slices.Reverse(results)
	messages := make([]ContextMessage, 0, len(snapshot.Context.Messages))
	for index := len(snapshot.Context.Messages) - 1; index >= 0; index-- {
		cost := llm.EstimateTokens(snapshot.Context.Messages[index].Body)
		if cost > budget {
			break
		}
		budget -= cost
		messages = append(messages, snapshot.Context.Messages[index])
	}
	slices.Reverse(messages)
	return map[string]any{
		"senders":        map[string]string{"customer": "提问人", "ai": "AI 员工", "staff": "真人处理人"},
		"context":        messages,
		"question":       snapshot.Question,
		"expectedAnswer": snapshot.ExpectedAnswer,
		"reply":          result.Content,
		"toolResults":    results,
	}
}
