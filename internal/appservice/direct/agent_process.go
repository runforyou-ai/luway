//go:build server

package direct

import (
	"context"
	"errors"

	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// GetAgentRunProcess 返回一次已完成运行的有序过程内容和模型用量。
func (o *conversationOps) GetAgentRunProcess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, runID string) (appservice.AgentRunProcess, error) {
	process, err := o.getAgentRunProcess.Execute(ctx, identity, runID)
	if err != nil {
		return appservice.AgentRunProcess{}, agentRunProcessError(meta, err)
	}
	result := appservice.AgentRunProcess{ID: process.ID, DurationMilliseconds: process.DurationMilliseconds,
		InputTokens: process.Usage.PromptTokens, OutputTokens: process.Usage.CompletionTokens,
		Outcome: process.Outcome, OutcomeReason: process.OutcomeReason,
		Blocks: make([]appservice.AgentRunContentBlock, 0, len(process.Blocks)),
		Plan: arr.Map(process.Plan, func(task stream.PlanTask) appservice.AgentPlanTask {
			return appservice.AgentPlanTask{ID: task.ID, Subject: task.Subject, Status: domain.AgentPlanTaskStatus(task.Status)}
		})}
	for _, block := range process.Blocks {
		item := appservice.AgentRunContentBlock{ID: block.ID, Position: block.Position, Kind: block.Kind, Text: block.Payload.Text}
		if call := block.Payload.ToolCall; call != nil {
			item.ToolCall = &appservice.AgentToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, BoundArguments: call.BoundArguments, Result: call.Result, Error: call.Error, Status: call.Status}
		}
		result.Blocks = append(result.Blocks, item)
	}
	return result, nil
}

// GetAgentToolCallProcess 返回电脑执行的工具调用的当前状态与给定序号之后的过程更新。
func (o *conversationOps) GetAgentToolCallProcess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, toolCallID string, input appservice.AgentToolCallProcessInput) (appservice.AgentToolCallProcess, error) {
	process, err := o.toolCallProcess.Execute(ctx, identity, toolCallID, input.From)
	if err != nil {
		return appservice.AgentToolCallProcess{}, toolCallProcessError(meta, err, i18n.ErrorAgentRunProcessReadFailed)
	}
	result := appservice.AgentToolCallProcess{Status: process.Status, Error: process.Error, LocalAgent: process.LocalAgent,
		Updates: make([]appservice.AgentToolCallUpdate, 0, len(process.Updates))}
	for _, update := range process.Updates {
		item := appservice.AgentToolCallUpdate{Seq: update.Seq, Kind: appservice.AgentToolCallUpdateKind(update.Update.Kind), Text: update.Update.Text,
			Plan: arr.Map(update.Update.Plan, func(entry domain.PlanStep) appservice.AgentPlanStep {
				return appservice.AgentPlanStep{Content: entry.Content, Status: entry.Status}
			}),
			Step: support.MapPtr(update.Update.Step, toolCallStepFromDomain)}
		result.Updates = append(result.Updates, item)
	}
	return result, nil
}

// StopLocalAgent 停止委派给本机 Agent 的一轮并释放它所在的本机 Agent 会话。
func (o *conversationOps) StopLocalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, toolCallID string) error {
	if err := o.stopLocalAgent.Execute(ctx, identity, toolCallID); err != nil {
		return toolCallProcessError(meta, err, i18n.ErrorLocalAgentStopFailed)
	}
	return nil
}

// toolCallProcessErrors 是工具调用过程读取与停止本机 Agent 的错误转换规则。
var toolCallProcessErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(processquery.ErrToolCallProcessUnavailable, dispatch.NotFound(i18n.ErrorLocalAgentTurnUnavailable)),
})

// toolCallProcessError 转换工具调用过程读取与停止本机 Agent 的错误。
func toolCallProcessError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return toolCallProcessErrors.Translate(meta, err, failureKey)
}

// toolCallStepFromDomain 转换本机 Agent 执行的一个步骤。
func toolCallStepFromDomain(step domain.ToolCallStep) appservice.AgentToolCallStep {
	result := appservice.AgentToolCallStep{ID: step.ID, Title: step.Title, Kind: step.Kind, Status: step.Status,
		Locations: append([]string{}, step.Locations...), Content: make([]appservice.AgentToolCallStepContent, 0, len(step.Content))}
	for _, content := range step.Content {
		item := appservice.AgentToolCallStepContent{Text: content.Text}
		if content.Diff != nil {
			item.Diff = &appservice.AgentFileDiff{Path: content.Diff.Path, OldText: content.Diff.OldText, NewText: content.Diff.NewText}
		}
		result.Content = append(result.Content, item)
	}
	return result
}

// GetAgentRunStreamState 读取持久状态并向实际执行实例请求快照，源暂不可用时保留运行状态。
func (o *conversationOps) GetAgentRunStreamState(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, runID string) (appservice.AgentRunStreamState, error) {
	run, err := o.authorizeAgentRunStream.State(ctx, identity, runID)
	if err != nil {
		return appservice.AgentRunStreamState{}, agentRunProcessError(meta, err)
	}
	result := appservice.AgentRunStreamState{Status: domain.AgentRunStatus(run.Status), Attempt: run.TaskAttempt}
	if run.Status != string(domain.AgentRunStatusRunning) || o.runSnapshots == nil || run.TaskInstanceID == nil {
		return result, nil
	}
	snapshot, err := o.runSnapshots.ReadRunSnapshot(ctx, *run.TaskInstanceID, runID, run.TaskAttempt)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if errors.Is(err, realtime.ErrRunSourceUnavailable) {
		return result, nil
	}
	if err != nil {
		return result, appservice.FailedError(meta, i18n.ErrorServerUnavailable, err)
	}
	current, err := o.authorizeAgentRunStream.State(ctx, identity, runID)
	if err != nil {
		return appservice.AgentRunStreamState{}, agentRunProcessError(meta, err)
	}
	result.Status, result.Attempt = domain.AgentRunStatus(current.Status), current.TaskAttempt
	if current.Status == string(domain.AgentRunStatusRunning) && current.TaskInstanceID != nil && *current.TaskInstanceID == *run.TaskInstanceID && current.TaskAttempt == run.TaskAttempt {
		view := protocol.RunStreamSnapshotView(snapshot.RunID, snapshot.Attempt, snapshot.Snapshot)
		result.Snapshot = &view
	}
	return result, nil
}

// agentRunProcessErrors 是运行过程详情读取的错误转换规则。
var agentRunProcessErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(processquery.ErrRunProcessUnavailable, dispatch.WithReason(dispatch.NotFound(i18n.ErrorAgentRunProcessUnavailable), "agent_run_process_unavailable")),
})

// agentRunProcessError 转换运行过程详情读取错误。
func agentRunProcessError(meta appservice.RequestMeta, err error) error {
	return agentRunProcessErrors.Translate(meta, err, i18n.ErrorAgentRunProcessReadFailed)
}

// conversationAgentProcessFromAction 转换已完成运行的过程引用、模型用量和结果。
func conversationAgentProcessFromAction(process processquery.Reference) appservice.ConversationAgentProcess {
	return appservice.ConversationAgentProcess{ID: process.ID, DurationMilliseconds: process.DurationMilliseconds,
		InputTokens: process.Usage.PromptTokens, OutputTokens: process.Usage.CompletionTokens,
		Outcome: process.Outcome, OutcomeReason: process.OutcomeReason}
}
