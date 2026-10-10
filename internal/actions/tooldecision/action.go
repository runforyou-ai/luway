//go:build server

package tooldecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrToolCallUnavailable 表示工具调用不存在或当前成员不是它的处理成员。
	ErrToolCallUnavailable = errors.New("agent tool call is unavailable")
	// ErrToolCallDecided 表示工具调用已经处理、已过期或当前状态不接受该操作。
	ErrToolCallDecided = errors.New("agent tool call has been decided")
)

// toolCallUnavailableMessage 是批准后业务系统、电脑、工具或授权已不允许执行时记录的失败原因。
const toolCallUnavailableMessage = "业务系统、电脑、工具或授权已变化，操作没有执行。"

// pausedCancelledResult 是暂停等待确认的调用被取消后交给模型的结果。
const pausedCancelledResult = "操作已取消，没有执行：确认人已无法处理这项操作。需要时向当前对方重新确认后再发起。"

// toolCallExecuteTimeout 是批准后执行一次业务系统调用的最长时间。
const toolCallExecuteTimeout = 2 * time.Minute

// Action 处理需要确认或审批的工具调用：成员与客户裁决、核对、批准后执行、过期、取消后的结果事件、本机 Agent 的权限请求，以及派发到电脑的调用有结果后的唤醒。
type Action struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scopes    RunScopes
	connector *businesssystem.Connector
}

// New 创建工具调用处理，scopes 提供运行所属会话的锁定、事件唤醒与挂起运行的恢复，业务系统调用使用默认连接器。
func New(db *bun.DB, enqueuer servertask.TxEnqueuer, scopes RunScopes) *Action {
	return &Action{db: db, enqueuer: enqueuer, scopes: scopes, connector: businesssystem.NewDefaultConnector()}
}

// Decide 由成员处理人确认、批准或拒绝等待裁决的工具调用。
func (a *Action) Decide(ctx context.Context, identity *servermodels.Identity, callID string, approve bool) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := a.lockToolCall(ctx, tx, identity.Workspace.ID, callID)
		if err != nil {
			return err
		}
		var subjectID string
		if err := tx.NewSelect().TableExpr("chat_subjects AS cs").Column("cs.id").
			Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
			Scan(ctx, &subjectID); errors.Is(err, sql.ErrNoRows) {
			return ErrToolCallUnavailable
		} else if err != nil {
			return fmt.Errorf("load member chat subject: %w", err)
		}
		return a.decide(ctx, tx, locked, subjectID, approve)
	})
}

// DecideForCustomer 在调用方已确认客户身份的事务中由客户确认或拒绝会话中等待其确认的工具调用；subjectID 是客户的聊天主体。
func (a *Action) DecideForCustomer(ctx context.Context, tx bun.Tx, workspaceID, conversationID, subjectID, callID string, approve bool) error {
	if !str.IsUUID(callID) {
		return ErrToolCallUnavailable
	}
	locked, err := a.lockToolCall(ctx, tx, workspaceID, callID)
	if err != nil {
		return err
	}
	if locked.Run.ConversationID != conversationID {
		return ErrToolCallUnavailable
	}
	return a.decide(ctx, tx, locked, subjectID, approve)
}

// decide 由处理人主体裁决已锁定的调用：本机 Agent 的权限请求把裁决结果交给电脑；电脑工具调用批准后交给电脑领取，电脑、授权已不允许执行或电脑离线时记为失败；
// 其余调用批准后投递执行；拒绝与失败以结果事件唤醒提交它的 AI 员工；暂停运行等待确认的调用只记下决定并恢复运行。
func (a *Action) decide(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectID string, approve bool) error {
	call := locked.Call
	if call.AssigneeSubjectID == nil || *call.AssigneeSubjectID != subjectID {
		return ErrToolCallUnavailable
	}
	// 截止判断取等待行锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, tx)
	if err != nil {
		return err
	}
	if call.Status != string(domain.AgentToolCallAwaitingDecision) || len(call.Decision) > 0 || call.ExpiresAt == nil || !call.ExpiresAt.After(now) {
		return ErrToolCallDecided
	}
	if call.Source == string(domain.AgentToolSourceLocalAgent) {
		return a.decideLocalAgentPermission(ctx, tx, locked, subjectID, approve)
	}
	if paused(call) {
		decision := einorun.CallDecision{Approved: approve, Reason: rejectedByDecider(approve)}
		// 确认执行前按当前配置复核电脑与业务系统授权，已不允许时按拒绝交给模型。
		if approve {
			failure, err := approvedFailure(ctx, tx, a.scopes, locked.Run, call)
			if err != nil {
				return err
			}
			if failure != "" {
				decision = einorun.CallDecision{Reason: failure}
			}
		}
		return a.decidePaused(ctx, tx, locked, &subjectID, decision)
	}
	status := domain.AgentToolCallQueued
	if !approve {
		status = domain.AgentToolCallRejected
	}
	update := tx.NewUpdate().Model(call).
		Set("status = ?", status).
		Set("decided_by_subject_id = ?", subjectID).
		Set("decided_at = now()")
	// 批准的电脑操作随即派发到电脑，记下审批请求的串联编号。
	if approve && call.ComputerID != nil {
		update = update.Set("trace_id = ?", support.NilIfZero(logscope.From(ctx).TraceID))
	}
	if _, err := update.WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("decide agent tool call: %w", err)
	}
	call.DecidedBySubjectID = &subjectID
	if !approve {
		return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallRejected, nil, nil)
	}
	workspaceID := call.WorkspaceID
	if call.ComputerID != nil {
		failure, err := checkApprovedComputerCall(ctx, tx, locked.Run, call)
		if err != nil {
			return err
		}
		if failure != "" {
			return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, &failure)
		}
		// 委派本机 Agent 的一轮批准后交给会话中该本机 Agent 的会话，其余电脑操作直接交给电脑领取。
		if call.Operation != nil && call.Operation.Operation.Kind == domain.ComputerOperationLocalAgent {
			var agentID string
			if err := tx.NewSelect().TableExpr("agents AS a").ColumnExpr("a.id::text").
				Where("a.workspace_id = ? AND a.identity_id = ?", workspaceID, locked.Run.AgentIdentityID).
				Scan(ctx, &agentID); err != nil {
				return fmt.Errorf("load approved local agent turn agent: %w", err)
			}
			online, err := localagent.Dispatch(ctx, tx, localagent.Turn{
				WorkspaceID: workspaceID, ConversationID: locked.Run.ConversationID, AgentID: agentID, ComputerID: *call.ComputerID,
				CallID: call.ID, Operation: call.Operation.Operation, From: domain.AgentToolCallQueued,
			})
			if err != nil {
				return err
			}
			if !online {
				return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, new(agentcontract.ErrComputerOffline.Error()))
			}
		}
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, *call.ComputerID))
		if err := agentprocess.NotifyDecisionSubjects(ctx, tx, workspaceID, subjectID); err != nil {
			return err
		}
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	}
	if _, err := a.enqueuer.EnqueueIn(ctx, ToolCallExecuteActionName, agentprocess.ToolCallInput{WorkspaceID: workspaceID, ToolCallID: call.ID}, servertask.EnqueueOptions{
		WorkspaceID: workspaceID, MaxAttempts: 3, IdempotencyKey: "agent-tool-call-execute:" + call.ID,
	}); err != nil {
		return fmt.Errorf("enqueue approved agent tool call: %w", err)
	}
	if err := agentprocess.NotifyDecisionSubjects(ctx, tx, workspaceID, subjectID); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
}

// Review 由 AI 员工的负责人把待核对的工具调用标记为已核对。
func (a *Action) Review(ctx context.Context, identity *servermodels.Identity, callID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := a.lockToolCall(ctx, tx, identity.Workspace.ID, callID)
		if err != nil {
			return err
		}
		approver, err := LoadApprover(ctx, tx, identity.Workspace.ID, locked.Run.AgentIdentityID)
		if err != nil {
			return err
		}
		if approver.UserID != identity.User.ID {
			return ErrToolCallUnavailable
		}
		if locked.Call.Status != string(domain.AgentToolCallNeedsReview) {
			return ErrToolCallDecided
		}
		subject, err := chatstate.EnsureSubject(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID, uuid.NewV7().String())
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(locked.Call).
			Set("status = ?", domain.AgentToolCallReviewed).
			Set("decided_by_subject_id = ?", subject.ID).
			Set("decided_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("review agent tool call: %w", err)
		}
		realtime.Notify(ctx, realtime.UserToolDecisionsChanged(identity.Workspace.ID, identity.User.ID))
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	})
}

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

// paused 判断调用是否暂停运行等待确认：由运行时推进，等待处理人的决定；本机 Agent 的权限请求不属于运行的调用。
func paused(call *servermodels.AgentToolCall) bool {
	return call.Handover == nil && call.Status == string(domain.AgentToolCallAwaitingDecision) && call.Source != string(domain.AgentToolSourceLocalAgent)
}

// rejectedByDecider 返回处理人拒绝时交给模型的原因，批准时为空。
func rejectedByDecider(approve bool) string {
	if approve {
		return ""
	}
	return "发起人拒绝执行这项操作。"
}

// decidePaused 记下暂停运行等待确认的调用的决定与处理人，通知处理人刷新待处理并恢复挂起的运行，运行恢复后据此在运行内执行或拒绝；过期时处理人为空。
func (a *Action) decidePaused(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectID *string, decision einorun.CallDecision) error {
	call := locked.Call
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode agent tool call decision: %w", err)
	}
	if _, err := tx.NewUpdate().Model(call).
		Set("decision = ?::jsonb", string(encoded)).
		Set("decided_by_subject_id = ?", subjectID).
		Set("decided_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("decide paused agent tool call: %w", err)
	}
	if call.AssigneeSubjectID != nil {
		if err := agentprocess.NotifyDecisionSubjects(ctx, tx, call.WorkspaceID, *call.AssigneeSubjectID); err != nil {
			return err
		}
	}
	if err := chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline); err != nil {
		return err
	}
	return a.scopes.ResumeWaitingRun(ctx, tx, call.WorkspaceID, locked.Run.ID)
}

// Expire 把截止时仍等待确认或审批的工具调用记为过期，并以结果事件唤醒提交它的 AI 员工；暂停运行等待确认的调用过期时记为拒绝并恢复运行。
func (a *Action) Expire(ctx context.Context, input agentprocess.ToolCallInput) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if errors.Is(err, ErrToolCallUnavailable) {
			return nil
		}
		if err != nil {
			return err
		}
		call := locked.Call
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if call.Status != string(domain.AgentToolCallAwaitingDecision) || len(call.Decision) > 0 || call.ExpiresAt == nil || call.ExpiresAt.After(now) {
			return nil
		}
		// 暂停运行等待确认的调用过期即拒绝，运行恢复后把原因交给模型。
		if paused(call) {
			return a.decidePaused(ctx, tx, locked, nil, einorun.CallDecision{Reason: "确认已过期，操作没有执行。"})
		}
		if _, err := tx.NewUpdate().Model(call).Set("decided_at = now()").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("expire agent tool call: %w", err)
		}
		return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallExpired, nil, nil)
	})
}

// Resolve 为已取消的工具调用写入结果事件，并在执行范围仍由提交它的 AI 员工处理时唤醒它；暂停运行等待确认的调用取消后恢复运行。
func (a *Action) Resolve(ctx context.Context, input agentprocess.ToolCallInput) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := a.lockToolCall(ctx, tx, input.WorkspaceID, input.ToolCallID)
		if errors.Is(err, ErrToolCallUnavailable) {
			return nil
		}
		if err != nil || locked.Call.Status != string(domain.AgentToolCallCancelled) {
			return err
		}
		// 本机 Agent 的权限请求取消后由执行器按取消处理，不唤醒 AI 员工。
		if locked.Call.Source == string(domain.AgentToolSourceLocalAgent) {
			return settleLocalAgentPermission(ctx, tx, locked)
		}
		// 暂停运行等待确认的调用取消后记下交给模型的说明并恢复运行。
		if locked.Call.Handover == nil {
			if _, err := tx.NewUpdate().Model(locked.Call).Set("result = ?", pausedCancelledResult).WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("record cancelled paused agent tool call: %w", err)
			}
			return a.scopes.ResumeWaitingRun(ctx, tx, locked.Call.WorkspaceID, locked.Run.ID)
		}
		return a.resolveToolCall(ctx, tx, locked)
	})
}

// List 按提交时间倒序返回待当前成员确认、审批或核对的工具调用。
func (a *Action) List(ctx context.Context, identity *servermodels.Identity) ([]agentprocess.ToolDecision, error) {
	return agentprocess.ListPendingToolDecisions(ctx, a.db, identity)
}
