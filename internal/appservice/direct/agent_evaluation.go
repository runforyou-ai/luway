//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// agentEvaluationOps 持有 AI 员工评测的查询与操作。
type agentEvaluationOps struct {
	getAgentEvaluation        *agentevaluationaction.OverviewQuery
	getAgentEvaluationCase    *agentevaluationaction.CaseDetailQuery
	addServiceIssueCase       *agentevaluationaction.AddServiceSessionCaseAction
	createAgentEvaluationCase *agentevaluationaction.CreateCaseAction
	updateAgentEvaluationCase *agentevaluationaction.UpdateCaseAction
	deleteAgentEvaluationCase *agentevaluationaction.DeleteCaseAction
	startAgentEvaluationRun   *agentevaluationaction.StartRunAction
	rerunAgentEvaluationCase  *agentevaluationaction.RerunCaseAction
}

// newAgentEvaluationOps 创建 AI 员工评测的业务实现依赖。
func newAgentEvaluationOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer) agentEvaluationOps {
	return agentEvaluationOps{
		getAgentEvaluation:        agentevaluationaction.NewOverviewQuery(db),
		getAgentEvaluationCase:    agentevaluationaction.NewCaseDetailQuery(db),
		addServiceIssueCase:       agentevaluationaction.NewAddServiceSessionCaseAction(db),
		createAgentEvaluationCase: agentevaluationaction.NewCreateCaseAction(db),
		updateAgentEvaluationCase: agentevaluationaction.NewUpdateCaseAction(db),
		deleteAgentEvaluationCase: agentevaluationaction.NewDeleteCaseAction(db),
		startAgentEvaluationRun:   agentevaluationaction.NewStartRunAction(db, taskEnqueuer),
		rerunAgentEvaluationCase:  agentevaluationaction.NewRerunCaseAction(db, taskEnqueuer),
	}
}

// GetAgentEvaluation 返回 AI 员工评测页的最近两次运行与全部用例。
func (o *directOperations) GetAgentEvaluation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.AgentEvaluation, error) {
	overview, err := o.getAgentEvaluation.Execute(ctx, identity, agentID)
	if err != nil {
		return appservice.AgentEvaluation{}, agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationLoadFailed)
	}
	output := appservice.AgentEvaluation{
		Latest: agentEvaluationRunSummary(overview.Latest), Previous: agentEvaluationRunSummary(overview.Previous),
		ConfigurationChanged: overview.ConfigurationChanged, DecisionModelReady: overview.DecisionModelReady,
		Cases: make([]appservice.AgentEvaluationCaseRow, 0, len(overview.Cases)),
	}
	for _, row := range overview.Cases {
		output.Cases = append(output.Cases, appservice.AgentEvaluationCaseRow{
			AgentEvaluationCase: agentEvaluationCase(row.Case),
			LatestStatus:        (*appservice.AgentEvaluationResultStatus)(row.LatestStatus),
			RerunStatus:         (*appservice.AgentEvaluationResultStatus)(row.RerunStatus),
			Modified:            row.Modified,
			NewFailure:          row.NewFailure,
		})
	}
	return output, nil
}

// StartAgentEvaluationRun 用 AI 员工当前生效的配置对全部用例发起一次评测运行。
func (o *directOperations) StartAgentEvaluationRun(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) error {
	runID, err := o.startAgentEvaluationRun.Execute(ctx, identity, agentID)
	if err != nil {
		return agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationStartFailed)
	}
	slog.Info("评测运行已发起", "organization_id", identity.Organization.ID, "agent_id", agentID, "evaluation_run_id", runID)
	return nil
}

// CreateAgentEvaluationCase 为 AI 员工新建手动评测用例。
func (o *directOperations) CreateAgentEvaluationCase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.AgentEvaluationCaseInput) (appservice.AgentEvaluationCase, error) {
	created, err := o.createAgentEvaluationCase.Execute(ctx, identity, agentID, agentEvaluationCaseInput(input))
	if err != nil {
		return appservice.AgentEvaluationCase{}, agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationCaseSaveFailed)
	}
	return agentEvaluationCase(*created), nil
}

// GetAgentEvaluationCase 返回评测用例与它在最近一次运行中的全部尝试。
func (o *directOperations) GetAgentEvaluationCase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, caseID string) (appservice.AgentEvaluationCaseDetail, error) {
	detail, err := o.getAgentEvaluationCase.Execute(ctx, identity, agentID, caseID)
	if err != nil {
		return appservice.AgentEvaluationCaseDetail{}, agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationLoadFailed)
	}
	output := appservice.AgentEvaluationCaseDetail{Case: agentEvaluationCase(detail.Case), Attempts: make([]appservice.AgentEvaluationAttempt, 0, len(detail.Attempts))}
	for _, attempt := range detail.Attempts {
		item := appservice.AgentEvaluationAttempt{
			ID: attempt.ID, Attempt: attempt.Attempt, CaseVersion: attempt.CaseVersion, Status: appservice.AgentEvaluationResultStatus(attempt.Status),
			Snapshot: appservice.AgentEvaluationSnapshot{
				Audience: appservice.ServiceAudience(attempt.Snapshot.Audience), Messages: agentEvaluationMessages(attempt.Snapshot.Context.Messages),
				Question: attempt.Snapshot.Question, ExpectedAction: appservice.AgentRunOutcome(attempt.Snapshot.ExpectedAction), ExpectedAnswer: attempt.Snapshot.ExpectedAnswer,
			},
			ActualAction: (*appservice.AgentRunOutcome)(attempt.ActualAction), ActualReason: (*appservice.AgentHandoffReason)(attempt.ActualReason),
			Answer: attempt.Answer, Blocks: make([]appservice.AgentRunContentBlock, 0, len(attempt.Blocks)),
			InputTokens: attempt.Usage.PromptTokens, OutputTokens: attempt.Usage.CompletionTokens, CorrectProbability: attempt.CorrectProbability,
			ErrorCode: (*appservice.AgentEvaluationErrorCode)(attempt.ErrorCode), CreatedAt: attempt.CreatedAt, CompletedAt: attempt.CompletedAt,
		}
		for _, block := range attempt.Blocks {
			content := appservice.AgentRunContentBlock{ID: block.ID, Position: block.Position, Kind: appservice.AgentRunBlockKind(block.Kind), Text: block.Payload.Text}
			if call := block.Payload.ToolCall; call != nil {
				content.ToolCall = &appservice.AgentToolCall{Name: call.Name, Arguments: call.Arguments, Result: call.Result, Error: call.Error, Status: appservice.AgentToolCallStatus(call.Status)}
			}
			item.Blocks = append(item.Blocks, content)
		}
		output.Attempts = append(output.Attempts, item)
	}
	return output, nil
}

// UpdateAgentEvaluationCase 修改评测用例。
func (o *directOperations) UpdateAgentEvaluationCase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, caseID string, input appservice.AgentEvaluationCaseInput) (appservice.AgentEvaluationCase, error) {
	updated, err := o.updateAgentEvaluationCase.Execute(ctx, identity, agentID, caseID, agentEvaluationCaseInput(input))
	if err != nil {
		return appservice.AgentEvaluationCase{}, agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationCaseSaveFailed)
	}
	return agentEvaluationCase(*updated), nil
}

// DeleteAgentEvaluationCase 删除评测用例。
func (o *directOperations) DeleteAgentEvaluationCase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, caseID string) error {
	if err := o.deleteAgentEvaluationCase.Execute(ctx, identity, agentID, caseID); err != nil {
		return agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationCaseDeleteFailed)
	}
	return nil
}

// RerunAgentEvaluationCase 在最近一次运行中重新运行一条用例。
func (o *directOperations) RerunAgentEvaluationCase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, caseID string) error {
	if err := o.rerunAgentEvaluationCase.Execute(ctx, identity, agentID, caseID); err != nil {
		return agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationRerunFailed)
	}
	return nil
}

// AddServiceIssueToEvaluation 把应转人工未转的问题会话以选定的客户消息为提问加入负责 AI 员工的评测。
func (o *directOperations) AddServiceIssueToEvaluation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, serviceSessionID string, input appservice.ServiceIssueEvaluationInput) (appservice.AgentEvaluationCase, error) {
	created, err := o.addServiceIssueCase.Execute(ctx, identity, serviceSessionID, input.QuestionMessageID)
	if err != nil {
		return appservice.AgentEvaluationCase{}, agentEvaluationError(meta, err, i18n.ErrorAgentEvaluationCaseSaveFailed)
	}
	slog.Info("问题会话已加入评测", "organization_id", identity.Organization.ID, "service_session_id", serviceSessionID, "evaluation_case_id", created.ID)
	return agentEvaluationCase(*created), nil
}

// agentEvaluationCaseInput 转换手动填写的评测用例。
func agentEvaluationCaseInput(input appservice.AgentEvaluationCaseInput) agentevaluationaction.CaseInput {
	return agentevaluationaction.CaseInput{
		Audience: domain.ServiceAudience(input.Audience), Question: input.Question,
		ExpectedAction: domain.AgentRunOutcome(input.ExpectedAction), ExpectedAnswer: input.ExpectedAnswer,
	}
}

// agentEvaluationCase 转换评测用例。
func agentEvaluationCase(evaluationCase agentevaluationaction.Case) appservice.AgentEvaluationCase {
	return appservice.AgentEvaluationCase{
		ID: evaluationCase.ID, AgentID: evaluationCase.AgentID, Source: appservice.AgentEvaluationCaseSource(evaluationCase.Source),
		Messages: agentEvaluationMessages(evaluationCase.Context.Messages), Version: evaluationCase.Version, Audience: appservice.ServiceAudience(evaluationCase.Audience),
		Question: evaluationCase.Question, ExpectedAction: appservice.AgentRunOutcome(evaluationCase.ExpectedAction), ExpectedAnswer: evaluationCase.ExpectedAnswer,
		CreatedAt: evaluationCase.CreatedAt, UpdatedAt: evaluationCase.UpdatedAt,
	}
}

// agentEvaluationMessages 转换用例前文。
func agentEvaluationMessages(messages []agentevaluationaction.ContextMessage) []appservice.AgentEvaluationContextMessage {
	output := make([]appservice.AgentEvaluationContextMessage, 0, len(messages))
	for _, message := range messages {
		output = append(output, appservice.AgentEvaluationContextMessage{Sender: appservice.AgentEvaluationContextSender(message.Sender), Body: message.Body})
	}
	return output
}

// agentEvaluationRunSummary 转换评测运行摘要。
func agentEvaluationRunSummary(summary *agentevaluationaction.RunSummary) *appservice.AgentEvaluationRunSummary {
	if summary == nil {
		return nil
	}
	return &appservice.AgentEvaluationRunSummary{
		ID: summary.ID, Status: appservice.AgentEvaluationRunStatus(summary.Status), CaseCount: summary.CaseCount, Finished: summary.Finished,
		Passed: summary.Passed, Failed: summary.Failed, Errors: summary.Errors, CreatedAt: summary.CreatedAt, CompletedAt: summary.CompletedAt,
	}
}

// agentEvaluationError 把评测错误转换为结构化、本地化错误。
func agentEvaluationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		fields := make(map[string]i18n.Key, len(validationError.Fields))
		for field, code := range validationError.Fields {
			fields[field] = map[common.FieldCode]i18n.Key{
				agentevaluationaction.ValidationAudienceInvalid:        i18n.FieldAgentEvaluationAudienceInvalid,
				agentevaluationaction.ValidationQuestionRequired:       i18n.FieldAgentEvaluationQuestionRequired,
				agentevaluationaction.ValidationExpectedActionInvalid:  i18n.FieldAgentEvaluationExpectedActionInvalid,
				agentevaluationaction.ValidationExpectedAnswerRequired: i18n.FieldAgentEvaluationExpectedAnswerRequired,
			}[code]
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, fields)
	}
	switch {
	case errors.Is(err, identityaction.ErrInvalid):
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	case errors.Is(err, agentevaluationaction.ErrAgentNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorAgentNotFound)
	case errors.Is(err, agentevaluationaction.ErrCaseNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorAgentEvaluationCaseNotFound)
	case errors.Is(err, agentevaluationaction.ErrRunInProgress):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationRunning, "agent_evaluation_running")
	case errors.Is(err, agentevaluationaction.ErrNoCases):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationNoCases, "agent_evaluation_no_cases")
	case errors.Is(err, agentevaluationaction.ErrDecisionModelUnavailable):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationDecisionModelRequired, "decision_model_required")
	case errors.Is(err, agentevaluationaction.ErrConfigurationUnavailable):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationConfigurationUnavailable, "agent_configuration_unavailable")
	case errors.Is(err, agentevaluationaction.ErrServiceSessionNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorServiceIssueNotFound)
	case errors.Is(err, agentevaluationaction.ErrQuestionNotFound):
		return appservice.InvalidError(meta, i18n.ErrorAgentEvaluationQuestionUnavailable, nil)
	case errors.Is(err, agentevaluationaction.ErrQuestionAlreadyEvaluated):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationQuestionAlreadyAdded, "agent_evaluation_question_already_added")
	case errors.Is(err, agentevaluationaction.ErrCaseChanged):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationCaseChanged, "agent_evaluation_case_changed")
	case errors.Is(err, agentevaluationaction.ErrResultNotFound):
		return appservice.ConflictError(meta, i18n.ErrorAgentEvaluationRerunUnavailable, "agent_evaluation_result_not_found")
	}
	return appservice.FailedError(meta, failureKey, err)
}
