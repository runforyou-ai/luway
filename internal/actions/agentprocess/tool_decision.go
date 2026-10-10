//go:build server

package agentprocess

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ToolDecision 是需要确认、审批或核对的工具调用的当前内容，以及当前成员可执行的处理与打开所在会话的位置。
type ToolDecision struct {
	ID                 string                   `bun:"id"`
	Name               string                   `bun:"name"`
	BusinessSystemName *string                  `bun:"business_system_name"`
	ComputerName       *string                  `bun:"computer_name"` // 电脑工具调用所派发电脑的名称，其他调用为空。
	LocalAgent         *string                  `bun:"local_agent"`   // 本机 Agent 权限请求所属的本机 Agent 名称，其他调用为空。
	Level              *domain.OperationLevel   `bun:"level"`
	Intervention       *domain.ToolIntervention `bun:"intervention"` // 自动执行的调用中断待核对时为空。
	Arguments          string                   `bun:"arguments"`    // 模型给出的参数，不含服务端绑定的参数。
	// ArgumentTitles 是参数定义中声明了标题的参数名到标题。
	ArgumentTitles  map[string]string          `bun:"argument_titles,type:jsonb"`
	Status          domain.AgentToolCallStatus `bun:"status"`
	Result          *string                    `bun:"result"` // 执行成功时的实际结果，其余状态为空。
	Error           *string                    `bun:"error"`
	AgentIdentityID string                     `bun:"agent_identity_id"`
	AgentName       string                     `bun:"agent_name"`
	AssigneeName    *string                    `bun:"assignee_name"`
	DecidedByName   *string                    `bun:"decided_by_name"`
	CreatedAt       time.Time                  `bun:"created_at"`
	ExpiresAt       *time.Time                 `bun:"expires_at"`
	DecidedAt       *time.Time                 `bun:"decided_at"`
	CanDecide       bool                       `bun:"can_decide"` // 当前成员是处理人且调用仍在截止前等待确认或审批，暂停运行的调用尚无决定。
	CanReview       bool                       `bun:"can_review"` // 当前成员是 AI 员工的负责人且调用待核对。
	// ConversationID 与 View 是打开调用所在会话的位置，Copilot 线程中的调用打开其所属的服务会话；ConversationReadable 表示当前成员可以阅读该会话。
	ConversationID       string                  `bun:"conversation_id"`
	View                 domain.NotificationView `bun:"view"`
	ConversationReadable bool                    `bun:"conversation_readable"`
}

// toolDecisionQuery 构造读取工具调用处理内容的查询，按当前成员计算可执行的处理。
func toolDecisionQuery(db bun.IDB, identity *servermodels.Identity) *bun.SelectQuery {
	return db.NewSelect().TableExpr("agent_tool_calls AS atc").
		ColumnExpr("atc.id, atc.name, atc.business_system_name, cmp.name AS computer_name, CASE WHEN atc.source = ? THEN las.local_agent END AS local_agent", domain.AgentToolSourceLocalAgent).
		ColumnExpr("atc.level, atc.intervention, atc.arguments, atc.error").
		// 暂停等待确认的调用有决定而运行尚未执行时，按批准为待执行、拒绝为已拒绝展示。
		ColumnExpr("CASE WHEN atc.status = ? AND atc.decision IS NOT NULL THEN CASE WHEN (atc.decision->>'approved')::boolean THEN ? ELSE ? END ELSE atc.status END AS status",
			domain.AgentToolCallAwaitingDecision, domain.AgentToolCallQueued, domain.AgentToolCallRejected).
		ColumnExpr(ArgumentTitles("atc")+" AS argument_titles").
		ColumnExpr("CASE WHEN atc.status = ? THEN atc.result END AS result", domain.AgentToolCallSucceeded).
		ColumnExpr("atc.created_at, atc.expires_at, atc.decided_at").
		ColumnExpr("agr.agent_identity_id, agent_oi.display_name AS agent_name").
		ColumnExpr(SubjectName("atc.assignee_subject_id")+" AS assignee_name, "+SubjectName("atc.decided_by_subject_id")+" AS decided_by_name").
		ColumnExpr("(atc.status = ? AND atc.decision IS NULL AND assignee_cs.kind = ? AND assignee_cs.source_id = ? AND atc.expires_at > now()) AS can_decide",
			domain.AgentToolCallAwaitingDecision, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
		ColumnExpr("(atc.status = ? AND a.responsible_user_id = ?) AS can_review", domain.AgentToolCallNeedsReview, identity.User.ID).
		ColumnExpr("COALESCE(sct.served_conversation_id, agr.conversation_id) AS conversation_id").
		ColumnExpr("CASE WHEN sct.conversation_id IS NOT NULL OR svc.conversation_id IS NOT NULL THEN ? WHEN cv.type = ? THEN ? WHEN cv.type = ? THEN ? ELSE ? END AS view",
			domain.NotificationViewService, domain.ConversationTypeAgent, domain.NotificationViewAgent, domain.ConversationTypeGroup, domain.NotificationViewGroup, domain.NotificationViewDirect).
		ColumnExpr(`CASE WHEN sct.conversation_id IS NOT NULL OR svc.conversation_id IS NOT NULL THEN TRUE ELSE EXISTS (
			SELECT 1 FROM conversation_participants AS reader_cp
			JOIN chat_subjects AS reader_cs ON reader_cs.id = reader_cp.subject_id AND reader_cs.workspace_id = reader_cp.workspace_id
			WHERE reader_cp.workspace_id = agr.workspace_id AND reader_cp.conversation_id = agr.conversation_id AND reader_cp.left_at IS NULL
				AND reader_cs.kind = ? AND reader_cs.source_id = ?) END AS conversation_readable`,
			domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
		Join("JOIN agent_runs AS agr ON agr.id = atc.agent_run_id AND agr.workspace_id = atc.workspace_id").
		Join("JOIN workspace_identities AS agent_oi ON agent_oi.id = agr.agent_identity_id AND agent_oi.workspace_id = agr.workspace_id").
		Join("JOIN agents AS a ON a.identity_id = agr.agent_identity_id AND a.workspace_id = agr.workspace_id").
		Join("JOIN conversations AS cv ON cv.id = agr.conversation_id AND cv.workspace_id = agr.workspace_id").
		Join("LEFT JOIN service_copilot_threads AS sct ON sct.conversation_id = agr.conversation_id AND sct.workspace_id = agr.workspace_id").
		Join("LEFT JOIN service_conversations AS svc ON svc.conversation_id = agr.conversation_id AND svc.workspace_id = agr.workspace_id").
		Join("LEFT JOIN computers AS cmp ON cmp.id = atc.computer_id AND cmp.workspace_id = atc.workspace_id").
		Join("LEFT JOIN local_agent_sessions AS las ON las.id = atc.local_agent_session_id AND las.workspace_id = atc.workspace_id").
		Join("LEFT JOIN chat_subjects AS assignee_cs ON assignee_cs.id = atc.assignee_subject_id AND assignee_cs.workspace_id = atc.workspace_id").
		Where("atc.workspace_id = ?", identity.Workspace.ID)
}

// ArgumentTitles 返回工具调用参数标题的 SQL 表达式：调用所属业务系统当前工具目录中该工具参数定义声明的标题，按参数名索引，没有时为空对象；call 是工具调用表别名。
func ArgumentTitles(call string) string {
	return fmt.Sprintf(`COALESCE((SELECT jsonb_object_agg(property.key, property.value->>'title') FROM business_systems AS bs,
		jsonb_array_elements(CASE WHEN jsonb_typeof(bs.tools) = 'array' THEN bs.tools ELSE '[]'::jsonb END) AS tool,
		jsonb_each(CASE WHEN jsonb_typeof(tool->'inputSchema'->'properties') = 'object' THEN tool->'inputSchema'->'properties' ELSE '{}'::jsonb END) AS property
		WHERE bs.workspace_id = %[1]s.workspace_id AND bs.id = %[1]s.business_system_id AND tool->>'name' = %[1]s.name
			AND jsonb_typeof(property.value->'title') = 'string'), '{}'::jsonb)`, call)
}

// SubjectName 返回聊天主体名称的 SQL 表达式：工作区身份取显示名称，联系人取成员界面的联系人名称；subjectID 是聊天主体编号的 SQL 表达式，为空时结果为 NULL。
func SubjectName(subjectID string) string {
	return fmt.Sprintf(`(SELECT CASE WHEN name_cs.kind = '%[2]s' THEN %[3]s ELSE name_oi.display_name END
		FROM chat_subjects AS name_cs
		LEFT JOIN workspace_identities AS name_oi ON name_oi.workspace_id = name_cs.workspace_id AND name_oi.id = name_cs.source_id AND name_cs.kind = '%[4]s'
		LEFT JOIN contacts AS name_c ON name_c.workspace_id = name_cs.workspace_id AND name_c.id = name_cs.source_id AND name_cs.kind = '%[2]s'
		WHERE name_cs.id = %[1]s)`, subjectID, domain.ChatSubjectKindContact, contactname.Expr("name_c", contactname.LatestIdentityName("name_c")), domain.ChatSubjectKindWorkspaceIdentity)
}

// LoadToolDecisions 按编号读取工具调用的处理内容，按编号索引返回。
func LoadToolDecisions(ctx context.Context, db bun.IDB, identity *servermodels.Identity, ids []string) (map[string]ToolDecision, error) {
	if len(ids) == 0 {
		return map[string]ToolDecision{}, nil
	}
	var rows []ToolDecision
	if err := toolDecisionQuery(db, identity).Where("atc.id IN (?)", bun.List(ids)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load agent tool decisions: %w", err)
	}
	return arr.KeyBy(rows, func(row ToolDecision) string { return row.ID }), nil
}

// ListPendingToolDecisions 按提交时间倒序读取待当前成员处理的工具调用：截止前等待本人确认或审批的调用，与本人负责的 AI 员工待核对的调用。
func ListPendingToolDecisions(ctx context.Context, db bun.IDB, identity *servermodels.Identity) ([]ToolDecision, error) {
	rows := make([]ToolDecision, 0)
	if err := toolDecisionQuery(db, identity).
		Where("(atc.status = ? AND atc.decision IS NULL AND assignee_cs.kind = ? AND assignee_cs.source_id = ? AND atc.expires_at > now()) OR (atc.status = ? AND a.responsible_user_id = ?)",
			domain.AgentToolCallAwaitingDecision, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID, domain.AgentToolCallNeedsReview, identity.User.ID).
		OrderExpr("atc.created_at DESC, atc.id DESC").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list pending agent tool decisions: %w", err)
	}
	return rows, nil
}

// NotifyReviewers 在调用转为待核对的事务中通知这些运行所属 AI 员工的负责人重新读取待处理操作，调用方必须处于 realtime.RunInTx 内。
func NotifyReviewers(ctx context.Context, db bun.IDB, workspaceID string, runIDs ...string) error {
	if len(runIDs) == 0 {
		return nil
	}
	var userIDs []string
	if err := db.NewSelect().TableExpr("agent_runs AS agr").
		ColumnExpr("DISTINCT a.responsible_user_id::text").
		Join("JOIN agents AS a ON a.identity_id = agr.agent_identity_id AND a.workspace_id = agr.workspace_id").
		Where("agr.workspace_id = ? AND agr.id IN (?) AND a.responsible_user_id IS NOT NULL", workspaceID, bun.List(runIDs)).
		Scan(ctx, &userIDs); err != nil {
		return fmt.Errorf("load agent tool call reviewers: %w", err)
	}
	for _, userID := range userIDs {
		realtime.Notify(ctx, realtime.UserToolDecisionsChanged(workspaceID, userID))
	}
	return nil
}

// ToolCallResolveActionName 为已取消的工具调用写入结果事件并唤醒提交它的 AI 员工。
const ToolCallResolveActionName = "agent.tool_call.resolve"

// ToolCallInput 定义工具调用任务的输入。
type ToolCallInput struct {
	WorkspaceID string `json:"workspaceId"`
	ToolCallID  string `json:"toolCallId"`
}

// NotifyDecisionSubjects 通知聊天主体中的成员重新读取待处理操作，联系人不接收该通知。
func NotifyDecisionSubjects(ctx context.Context, db bun.IDB, workspaceID string, subjectIDs ...string) error {
	if len(subjectIDs) == 0 {
		return nil
	}
	var userIDs []string
	if err := db.NewSelect().TableExpr("chat_subjects AS cs").
		ColumnExpr("DISTINCT u.id::text").
		Join("JOIN users AS u ON u.workspace_id = cs.workspace_id AND u.identity_id = cs.source_id").
		Where("cs.workspace_id = ? AND cs.id IN (?) AND cs.kind = ?", workspaceID, bun.List(subjectIDs), domain.ChatSubjectKindWorkspaceIdentity).
		Scan(ctx, &userIDs); err != nil {
		return fmt.Errorf("load tool decision members: %w", err)
	}
	for _, userID := range userIDs {
		realtime.Notify(ctx, realtime.UserToolDecisionsChanged(workspaceID, userID))
	}
	return nil
}

// cancelToolDecisions 把满足条件的等待确认或审批的调用记为已取消，通知原处理成员刷新待处理操作，并投递写入结果事件与唤醒的任务；
// filter 在别名 atc（工具调用）与 agr（所属运行）上追加条件。调用方必须处于 realtime.RunInTx 内。
func cancelToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID string, filter func(*bun.SelectQuery) *bun.SelectQuery) error {
	calls := filter(db.NewSelect().TableExpr("agent_tool_calls AS atc").
		Column("atc.id").
		Join("JOIN agent_runs AS agr ON agr.id = atc.agent_run_id AND agr.workspace_id = atc.workspace_id").
		Where("atc.workspace_id = ? AND atc.status = ?", workspaceID, domain.AgentToolCallAwaitingDecision))
	var cancelled []struct {
		ID                string  `bun:"id"`
		AssigneeSubjectID *string `bun:"assignee_subject_id"`
	}
	if err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallCancelled).
		Set("decided_at = now()").
		Set("completed_at = now()").
		Where("workspace_id = ? AND status = ? AND id IN (?)", workspaceID, domain.AgentToolCallAwaitingDecision, calls).
		Returning("id, assignee_subject_id").
		Scan(ctx, &cancelled); err != nil {
		return fmt.Errorf("cancel agent tool call decisions: %w", err)
	}
	if len(cancelled) == 0 {
		return nil
	}
	requests := make([]servertask.EnqueueRequest, 0, len(cancelled))
	subjectIDs := make([]string, 0, len(cancelled))
	for _, call := range cancelled {
		requests = append(requests, servertask.EnqueueRequest{
			ActionName: ToolCallResolveActionName, Payload: ToolCallInput{WorkspaceID: workspaceID, ToolCallID: call.ID},
			Options: servertask.EnqueueOptions{
				WorkspaceID: workspaceID, MaxAttempts: 3, IdempotencyKey: "agent-tool-call-resolve:" + call.ID,
			},
		})
		if call.AssigneeSubjectID != nil {
			subjectIDs = append(subjectIDs, *call.AssigneeSubjectID)
		}
	}
	if err := enqueuer.EnqueueManyIn(ctx, requests); err != nil {
		return fmt.Errorf("enqueue cancelled agent tool call resolution: %w", err)
	}
	return NotifyDecisionSubjects(ctx, db, workspaceID, subjectIDs...)
}

// CancelMemberToolDecisions 取消等待指定成员确认或审批的调用，用于成员停用。
func CancelMemberToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, identityID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("atc.assignee_subject_id IN (SELECT cs.id FROM chat_subjects AS cs WHERE cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?)",
			workspaceID, domain.ChatSubjectKindWorkspaceIdentity, identityID)
	})
}

// CancelConversationParticipantToolDecisions 取消会话中与离开的参与方相关的调用：等待该成员确认的调用，以及该 AI 员工提交的调用，用于成员或 AI 员工离开会话。
func CancelConversationParticipantToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, conversationID, identityID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("agr.conversation_id = ?", conversationID).
			WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
				return query.Where("agr.agent_identity_id = ?", identityID).
					WhereOr("atc.intervention = ? AND atc.assignee_subject_id IN (SELECT cs.id FROM chat_subjects AS cs WHERE cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?)",
						domain.ToolInterventionConfirmation, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, identityID)
			})
	})
}

// CancelAgentToolDecisions 取消 AI 员工提交的全部等待确认或审批的调用，用于 AI 员工停用。
func CancelAgentToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, agentIdentityID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("agr.agent_identity_id = ?", agentIdentityID)
	})
}

// CancelReassignedApprovals 取消 AI 员工提交的、处理人不再是其在职负责人的等待审批的调用，用于负责人变更。
func CancelReassignedApprovals(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, agentIdentityID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("agr.agent_identity_id = ? AND atc.intervention = ?", agentIdentityID, domain.ToolInterventionApproval).
			Where(`atc.assignee_subject_id IS DISTINCT FROM (SELECT cs.id FROM agents AS a
				JOIN users AS u ON u.workspace_id = a.workspace_id AND u.id = a.responsible_user_id AND u.status = ?
				JOIN chat_subjects AS cs ON cs.workspace_id = u.workspace_id AND cs.kind = ? AND cs.source_id = u.identity_id
				WHERE a.workspace_id = agr.workspace_id AND a.identity_id = agr.agent_identity_id)`, domain.IdentityStatusActive, domain.ChatSubjectKindWorkspaceIdentity)
	})
}

// CancelServiceSessionToolDecisions 取消服务周期中不再由提交它的 AI 员工处理的调用：周期已结束，或负责人已不是该 AI 员工。
func CancelServiceSessionToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, serviceSessionID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("agr.scope_kind = ? AND agr.scope_id = ?", domain.AgentExecutionScopeServiceSession, serviceSessionID).
			Where(`NOT EXISTS (SELECT 1 FROM service_sessions AS ss WHERE ss.workspace_id = agr.workspace_id AND ss.id = agr.scope_id
				AND ss.status <> ? AND ss.assignee_identity_id = agr.agent_identity_id)`, domain.ServiceSessionStatusClosed)
	})
}

// CancelChannelIdentityToolDecisions 取消渠道身份的客户会话中等待客户确认的调用，用于客户核验身份变化。
func CancelChannelIdentityToolDecisions(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, channelIdentityID string) error {
	return cancelToolDecisions(ctx, db, enqueuer, workspaceID, func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Where("atc.intervention = ?", domain.ToolInterventionConfirmation).
			Where("atc.assignee_subject_id IN (SELECT cs.id FROM chat_subjects AS cs WHERE cs.workspace_id = ? AND cs.kind = ?)", workspaceID, domain.ChatSubjectKindContact).
			Where("agr.conversation_id IN (SELECT cc.conversation_id FROM channel_conversations AS cc WHERE cc.workspace_id = ? AND cc.channel_identity_id = ?)", workspaceID, channelIdentityID)
	})
}
