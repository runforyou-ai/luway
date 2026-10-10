//go:build server

package direct

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// toolDecisionOps 是 AI 员工操作确认、审批与核对的业务实现依赖。
type toolDecisionOps struct {
	toolDecisions *tooldecisionaction.Action
}

// newToolDecisionOps 创建 AI 员工操作确认、审批与核对的业务实现依赖。
func newToolDecisionOps(toolDecisions *tooldecisionaction.Action) *toolDecisionOps {
	return &toolDecisionOps{toolDecisions: toolDecisions}
}

// ListAgentToolDecisions 返回待当前成员确认、审批或核对的 AI 员工操作。
func (o *toolDecisionOps) ListAgentToolDecisions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AgentToolDecisionList, error) {
	decisions, err := o.toolDecisions.List(ctx, identity)
	if err != nil {
		return appservice.AgentToolDecisionList{}, appservice.FailedError(meta, i18n.ErrorToolDecisionsLoadFailed, err)
	}
	return appservice.AgentToolDecisionList{Items: arr.Map(decisions, func(decision agentprocess.ToolDecision) appservice.AgentToolDecision {
		return agentToolDecision(ctx, decision)
	})}, nil
}

// DecideAgentToolCall 由处理成员确认、批准或拒绝 AI 员工提交的操作。
func (o *toolDecisionOps) DecideAgentToolCall(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, toolCallID string, input appservice.AgentToolDecisionInput) error {
	return toolDecisionError(meta, o.toolDecisions.Decide(ctx, identity, toolCallID, input.Approve))
}

// ReviewAgentToolCall 由 AI 员工的负责人把结果未知的操作标记为已核对。
func (o *toolDecisionOps) ReviewAgentToolCall(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, toolCallID string) error {
	return toolDecisionError(meta, o.toolDecisions.Review(ctx, identity, toolCallID))
}

// toolDecisionErrors 是操作裁决与核对的错误转换规则。
var toolDecisionErrors = dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(tooldecisionaction.ErrToolCallUnavailable, dispatch.NotFound(i18n.ErrorToolCallUnavailable)),
	dispatch.Is(tooldecisionaction.ErrToolCallDecided, dispatch.Conflict(i18n.ErrorToolCallDecided, "tool_call_decided")),
}

// toolDecisionError 转换操作裁决与核对的错误。
func toolDecisionError(meta appservice.RequestMeta, err error) error {
	return toolDecisionErrors.Translate(meta, err, i18n.ErrorToolDecisionFailed)
}

// agentToolDecision 把工具调用的处理内容转换为传输契约；本机 Agent 的权限请求以请求的步骤代替参数与结果。
func agentToolDecision(ctx context.Context, decision agentprocess.ToolDecision) appservice.AgentToolDecision {
	arguments, result := decision.Arguments, decision.Result
	var permission *appservice.AgentLocalAgentPermission
	if decision.LocalAgent != nil {
		var stored domain.LocalAgentPermission
		if err := json.Unmarshal([]byte(decision.Arguments), &stored); err == nil {
			permission = &appservice.AgentLocalAgentPermission{Step: toolCallStepFromDomain(stored.Step)}
		} else {
			slog.WarnContext(ctx, "解析本机 Agent 权限请求失败", "tool_call_id", decision.ID, "error", err)
		}
		arguments, result = "{}", nil
	}
	return appservice.AgentToolDecision{
		ID: decision.ID, Name: decision.Name, BusinessSystemName: decision.BusinessSystemName, ComputerName: decision.ComputerName,
		LocalAgent: decision.LocalAgent, Permission: permission,
		Level: decision.Level, Intervention: (*appservice.ToolIntervention)(decision.Intervention),
		Arguments: arguments, ArgumentTitles: decision.ArgumentTitles, Status: decision.Status, Result: result, Error: decision.Error,
		AgentIdentityID: decision.AgentIdentityID, AgentName: decision.AgentName, AssigneeName: decision.AssigneeName, DecidedByName: decision.DecidedByName,
		CreatedAt: decision.CreatedAt, ExpiresAt: decision.ExpiresAt, DecidedAt: decision.DecidedAt,
		CanDecide: decision.CanDecide, CanReview: decision.CanReview,
		ConversationID: decision.ConversationID, View: decision.View, ConversationReadable: decision.ConversationReadable,
	}
}
