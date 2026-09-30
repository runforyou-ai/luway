//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// GetAgentRunProcess 返回一次已完成运行的有序过程内容和模型用量。
func (o *directOperations) GetAgentRunProcess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, runID string) (appservice.AgentRunProcess, error) {
	process, err := o.getAgentRunProcess.Execute(ctx, identity, runID)
	if err != nil {
		return appservice.AgentRunProcess{}, agentRunProcessError(ctx, meta, err, identity.Organization.ID, runID)
	}
	result := appservice.AgentRunProcess{ID: process.ID, DurationMilliseconds: process.DurationMilliseconds,
		InputTokens: process.Usage.PromptTokens, OutputTokens: process.Usage.CompletionTokens,
		Outcome: (*appservice.AgentRunOutcome)(process.Outcome), OutcomeReason: (*appservice.AgentHandoffReason)(process.OutcomeReason),
		Blocks: make([]appservice.AgentRunContentBlock, 0, len(process.Blocks)), Plan: make([]appservice.AgentPlanTask, 0, len(process.Plan))}
	for _, block := range process.Blocks {
		item := appservice.AgentRunContentBlock{ID: block.ID, Position: block.Position, Kind: appservice.AgentRunBlockKind(block.Kind), Text: block.Payload.Text}
		if call := block.Payload.ToolCall; call != nil {
			item.ToolCall = &appservice.AgentToolCall{Name: call.Name, Arguments: call.Arguments, Result: call.Result, Error: call.Error, Status: appservice.AgentToolCallStatus(call.Status)}
		}
		result.Blocks = append(result.Blocks, item)
	}
	for _, task := range process.Plan {
		result.Plan = append(result.Plan, appservice.AgentPlanTask{ID: task.ID, Subject: task.Subject, Status: appservice.AgentPlanTaskStatus(task.Status)})
	}
	return result, nil
}

// AuthorizeAgentRunStreamAccess 校验当前成员对运行所属会话的阅读资格。
func (o *directOperations) AuthorizeAgentRunStreamAccess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, runID string) error {
	if _, err := o.authorizeAgentRunStream.Execute(ctx, identity, runID); err != nil {
		return agentRunProcessError(ctx, meta, err, identity.Organization.ID, runID)
	}
	return nil
}

// agentRunProcessError 转换运行过程详情读取错误。
func agentRunProcessError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, runID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, conversationaction.ErrAgentRunProcessUnavailable) {
		return appservice.NotFoundError(meta, i18n.ErrorAgentRunProcessUnavailable).WithReason("agent_run_process_unavailable")
	}
	slog.Warn("读取 AI 运行过程失败", "organization_id", organizationID, "agent_run_id", runID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorAgentRunProcessReadFailed)
}

// conversationAgentProcessFromAction 转换已完成运行的过程引用、模型用量和结果。
func conversationAgentProcessFromAction(process *conversationaction.ConversationAgentProcess) *appservice.ConversationAgentProcess {
	if process == nil {
		return nil
	}
	return &appservice.ConversationAgentProcess{ID: process.ID, DurationMilliseconds: process.DurationMilliseconds,
		InputTokens: process.Usage.PromptTokens, OutputTokens: process.Usage.CompletionTokens,
		Outcome: (*appservice.AgentRunOutcome)(process.Outcome), OutcomeReason: (*appservice.AgentHandoffReason)(process.OutcomeReason)}
}
