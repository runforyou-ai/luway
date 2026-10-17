//go:build server

package tooldecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ExecuteApproved 按提交时的实际参数执行已批准的工具调用：业务系统、工具或授权已不允许执行时记为失败；上次执行在调用期间中断时按外部副作用记为待核对；
// 执行结果作为事件唤醒提交它的 AI 员工。
func (a *Action) ExecuteApproved(ctx context.Context, input agentprocess.ToolCallInput) error {
	prepared, err := prepareApproved(ctx, a.db, a.scopes, input)
	if err != nil {
		return err
	}
	execute := false
	if err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if err != nil {
			return err
		}
		switch domain.AgentToolCallStatus(locked.Call.Status) {
		case domain.AgentToolCallRunning:
			status, result := agentcontract.InterruptedStatus(locked.Call.Replayable, locked.Call.SideEffects)
			if status == domain.AgentToolCallNeedsReview && prepared.approver != "" {
				realtime.Notify(ctx, realtime.UserToolDecisionsChanged(input.WorkspaceID, prepared.approver))
			}
			return a.settleDecidedToolCall(ctx, tx, locked, status, &result, nil)
		case domain.AgentToolCallQueued:
		default:
			return nil
		}
		if prepared.tool == nil {
			return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, new(toolCallUnavailableMessage))
		}
		if _, err := tx.NewUpdate().Model(locked.Call).
			Set("status = ?", domain.AgentToolCallRunning).
			Set("started_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("start approved agent tool call: %w", err)
		}
		execute = true
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	}); err != nil || !execute {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, toolCallExecuteTimeout)
	defer cancel()
	caller := a.connector.Open(callCtx, prepared.system, prepared.headers)
	defer func() { _ = caller.Close() }()
	result, callErr := caller.Call(callCtx, prepared.tool.Name, prepared.arguments)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if err != nil {
			return err
		}
		if locked.Call.Status != string(domain.AgentToolCallRunning) {
			return nil
		}
		if callErr != nil {
			return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, new(callErr.Error()))
		}
		return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallSucceeded, &result, nil)
	})
}

// checkApprovedComputerCall 确认批准的电脑工具调用仍可派发：AI 员工仍使用提交时的电脑、电脑未撤销、工作区电脑的授权仍允许调用的级别、委派的本机 Agent 仍启用且电脑在线；
// 不可派发时返回失败原因。
func checkApprovedComputerCall(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun, call *servermodels.AgentToolCall) (string, error) {
	var target struct {
		ComputerID  *string           `bun:"computer_id"`
		Grant       *domain.ToolGrant `bun:"computer_grant,type:jsonb"`
		LocalAgents []string          `bun:"local_agents,type:jsonb"`
		Kind        string            `bun:"kind"`
		Revoked     bool              `bun:"revoked"`
		Online      bool              `bun:"online"`
	}
	err := tx.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.computer_id::text, a.computer_grant, a.local_agents, cmp.kind, cmp.revoked_at IS NOT NULL AS revoked").
		ColumnExpr("COALESCE(cmp.last_seen_at > now() - make_interval(secs => ?), false) AS online", domain.ComputerPresenceTimeout.Seconds()).
		Join("JOIN computers AS cmp ON cmp.id = a.computer_id AND cmp.workspace_id = a.workspace_id").
		Where("a.workspace_id = ? AND a.identity_id = ?", run.WorkspaceID, run.AgentIdentityID).
		For("SHARE OF cmp").
		Scan(ctx, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return toolCallUnavailableMessage, nil
	}
	if err != nil {
		return "", fmt.Errorf("load approved computer tool call target: %w", err)
	}
	permitted := domain.ComputerKind(target.Kind) == domain.ComputerKindPersonal
	if domain.ComputerKind(target.Kind) == domain.ComputerKindWorkspace && target.Grant != nil && call.Level != nil {
		permitted, _ = target.Grant.Permit(domain.OperationLevel(*call.Level))
	}
	if operation := call.Operation; operation != nil && operation.Operation.Kind == domain.ComputerOperationLocalAgent && !slices.Contains(target.LocalAgents, operation.Operation.LocalAgent) {
		permitted = false
	}
	switch {
	case target.ComputerID == nil || *target.ComputerID != *call.ComputerID || target.Revoked || !permitted:
		return toolCallUnavailableMessage, nil
	case !target.Online:
		return agentcontract.ErrComputerOffline.Error(), nil
	}
	return "", nil
}

// resolveApprovedCall 在批准后派发到电脑的调用已有结果的事务中写入结果事件并唤醒提交它的 AI 员工，调用方已锁定调用所属会话。
func (a *Action) resolveApprovedCall(ctx context.Context, tx bun.Tx, workspaceID, callID string) error {
	locked, err := a.lockToolCall(ctx, tx, workspaceID, callID)
	if err != nil {
		return err
	}
	return a.resolveToolCall(ctx, tx, locked)
}

// approvedCall 是执行已批准的工具调用所需的业务系统、挂载的工具、请求头、提交时的模型参数与绑定参数合成的实际参数与 AI 员工负责人；
// 业务系统或授权已不允许执行时工具为空。
type approvedCall struct {
	system    servermodels.BusinessSystem
	tool      *agentcontract.BusinessToolMount
	headers   map[string]string
	arguments json.RawMessage
	approver  string
}

// prepareApproved 按 AI 员工当前生效的授权与原运行的可信上下文值重新挂载调用的业务系统，确认工具仍可执行并取得调用会话所需的请求头。
func prepareApproved(ctx context.Context, db bun.IDB, scopes RunScopes, input agentprocess.ToolCallInput) (approvedCall, error) {
	call := &servermodels.AgentToolCall{}
	err := db.NewSelect().Model(call).Where("atc.workspace_id = ? AND atc.id = ?", input.WorkspaceID, input.ToolCallID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return approvedCall{}, servertask.Permanent(ErrToolCallUnavailable)
	}
	if err != nil {
		return approvedCall{}, fmt.Errorf("load approved agent tool call: %w", err)
	}
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Where("agr.workspace_id = ? AND agr.id = ?", input.WorkspaceID, call.AgentRunID).Scan(ctx); err != nil {
		return approvedCall{}, fmt.Errorf("load approved agent tool call run: %w", err)
	}
	prepared := approvedCall{}
	approver, err := LoadApprover(ctx, db, input.WorkspaceID, run.AgentIdentityID)
	if err != nil {
		return approvedCall{}, err
	}
	prepared.approver = approver.UserID
	// 绑定参数保持提交时的值；参数无法合成时按业务系统已不允许执行处理。
	if prepared.arguments, err = agentcontract.BindArguments(json.RawMessage(call.Arguments), call.BoundArguments); err != nil {
		return prepared, nil
	}
	if call.BusinessSystemID == nil {
		return prepared, nil
	}
	err = db.NewSelect().Model(&prepared.system).Where("bs.workspace_id = ? AND bs.id = ?", input.WorkspaceID, *call.BusinessSystemID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return prepared, nil
	}
	if err != nil {
		return approvedCall{}, fmt.Errorf("load approved agent tool call business system: %w", err)
	}
	var configuration struct {
		Grants []domain.BusinessSystemGrant `bun:"business_systems,type:jsonb"`
	}
	if err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("ar.configuration->'businessSystems' AS business_systems").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.workspace_id = a.workspace_id").
		Where("a.workspace_id = ? AND a.identity_id = ?", input.WorkspaceID, run.AgentIdentityID).
		Scan(ctx, &configuration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return approvedCall{}, fmt.Errorf("load agent business system grants: %w", err)
	}
	grants := configuration.Grants
	values, err := scopes.RunValues(ctx, db, run)
	if err != nil {
		return approvedCall{}, err
	}
	for _, grant := range grants {
		if grant.BusinessSystemID != prepared.system.ID {
			continue
		}
		mounted, headers, _ := businesssystem.Mount(prepared.system, grant, businesssystem.MountOptions{
			Grants: grants, Values: values, Interventions: []domain.ToolIntervention{domain.ToolInterventionConfirmation, domain.ToolInterventionApproval},
		})
		for index := range mounted.Tools {
			if mounted.Tools[index].Name == call.Name {
				prepared.tool, prepared.headers = &mounted.Tools[index], headers
			}
		}
	}
	return prepared, nil
}

// approvedFailure 按当前配置复核批准的调用：派发到电脑的调用确认电脑与授权仍允许，业务系统的调用确认工具仍可挂载；仍可执行时返回空，否则返回交给模型的原因。
func approvedFailure(ctx context.Context, tx bun.Tx, scopes RunScopes, run *servermodels.AgentRun, call *servermodels.AgentToolCall) (string, error) {
	if call.ComputerID != nil {
		failure, err := checkApprovedComputerCall(ctx, tx, run, call)
		if err != nil || failure != "" {
			return failure, err
		}
	}
	if call.BusinessSystemID != nil {
		prepared, err := prepareApproved(ctx, tx, scopes, agentprocess.ToolCallInput{WorkspaceID: call.WorkspaceID, ToolCallID: call.ID})
		if err != nil {
			return "", err
		}
		if prepared.tool == nil {
			return toolCallUnavailableMessage, nil
		}
	}
	return "", nil
}

// CheckDecidedCall 在暂停确认后的调用于运行内实际执行前按当前配置复核：依次共享锁定所用电脑与 AI 员工后确认电脑或业务系统授权仍允许，且工具所需的人工介入与确认时相同；
// 不是暂停确认后执行的调用返回空，已不允许时返回交给模型的原因。授权变更锁定同一 AI 员工，复核与之串行。
func CheckDecidedCall(ctx context.Context, db *bun.DB, scopes RunScopes, workspaceID, callID string) (string, error) {
	failure := ""
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		call := &servermodels.AgentToolCall{}
		err := tx.NewSelect().Model(call).
			Where("atc.workspace_id = ? AND atc.id = ?", workspaceID, callID).
			Where("atc.handover IS NULL AND atc.source <> ? AND (atc.decision->>'approved')::boolean", domain.AgentToolSourceLocalAgent).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load decided agent tool call: %w", err)
		}
		run := &servermodels.AgentRun{}
		if err := tx.NewSelect().Model(run).Where("agr.workspace_id = ? AND agr.id = ?", workspaceID, call.AgentRunID).Scan(ctx); err != nil {
			return fmt.Errorf("load decided agent tool call run: %w", err)
		}
		// 与撤销电脑、保存执行配置的锁顺序一致：先电脑，后 AI 员工。
		if call.ComputerID != nil {
			if err := tx.NewSelect().TableExpr("computers AS cmp").ColumnExpr("cmp.id::text").
				Where("cmp.workspace_id = ? AND cmp.id = ?", workspaceID, *call.ComputerID).
				For("SHARE").Scan(ctx, new(string)); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("lock decided agent tool call computer: %w", err)
			}
		}
		var agent struct {
			ComputerID *string `bun:"computer_id"`
		}
		if err := tx.NewSelect().TableExpr("agents AS a").ColumnExpr("a.computer_id::text").
			Where("a.workspace_id = ? AND a.identity_id = ?", workspaceID, run.AgentIdentityID).
			For("SHARE").Scan(ctx, &agent); errors.Is(err, sql.ErrNoRows) {
			failure = toolCallUnavailableMessage
			return nil
		} else if err != nil {
			return fmt.Errorf("lock decided agent tool call agent: %w", err)
		}
		// AI 员工已改用其他电脑时原确认不再适用，不再锁定新电脑。
		if call.ComputerID != nil && (agent.ComputerID == nil || *agent.ComputerID != *call.ComputerID) {
			failure = toolCallUnavailableMessage
			return nil
		}
		if call.ComputerID != nil {
			if failure, err = checkApprovedComputerCall(ctx, tx, run, call); err != nil || failure != "" {
				return err
			}
		}
		if call.BusinessSystemID == nil {
			return nil
		}
		prepared, err := prepareApproved(ctx, tx, scopes, agentprocess.ToolCallInput{WorkspaceID: workspaceID, ToolCallID: callID})
		if err != nil {
			return err
		}
		// 工具已不可挂载，或所需的人工介入与确认时不同，原确认不再适用。
		if prepared.tool == nil || call.Intervention == nil || string(prepared.tool.Intervention) != *call.Intervention {
			failure = toolCallUnavailableMessage
		}
		return nil
	})
	return failure, err
}
