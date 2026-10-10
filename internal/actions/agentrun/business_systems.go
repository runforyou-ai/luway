//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// runAudience 是运行的服务对象：是否服务客户与本次运行有处理人可以处理的人工介入。
type runAudience struct {
	customer      bool
	interventions []domain.ToolIntervention
}

// loadRunAudience 读取运行是否服务客户与可处理的人工介入：服务客户的周期在客户已核验且渠道能展示确认卡片时由客户确认，其余执行范围由发起成员确认；
// AI 员工有在职负责人时由负责人审批。
func loadRunAudience(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (runAudience, error) {
	audience := runAudience{interventions: make([]domain.ToolIntervention, 0, 2)}
	confirmable := true
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
		if err != nil {
			return runAudience{}, err
		}
		audience.customer = domain.ServiceAudience(service.Audience) == domain.ServiceAudienceCustomer
		if audience.customer {
			if confirmable, err = customerConfirmable(ctx, db, run); err != nil {
				return runAudience{}, err
			}
		}
	}
	if confirmable {
		audience.interventions = append(audience.interventions, domain.ToolInterventionConfirmation)
	}
	approver, err := tooldecision.LoadApprover(ctx, db, run.WorkspaceID, run.AgentIdentityID)
	if err != nil {
		return runAudience{}, err
	}
	if approver.UserID != "" {
		audience.interventions = append(audience.interventions, domain.ToolInterventionApproval)
	}
	return audience, nil
}

// customerConfirmable 判断服务客户的周期能由客户确认操作：客户已核验，且会话所在渠道能展示确认卡片。
func customerConfirmable(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (bool, error) {
	customer, err := LoadServiceSessionCustomer(ctx, db, run.WorkspaceID, run.ScopeID)
	if err != nil || customer.UserID == "" {
		return false, err
	}
	var channelType domain.ChannelType
	if err := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("ch.type").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Where("cc.workspace_id = ? AND cc.conversation_id = ?", run.WorkspaceID, run.ConversationID).
		Scan(ctx, &channelType); err != nil {
		return false, fmt.Errorf("load customer conversation channel type: %w", err)
	}
	return domain.ChannelCapabilitiesOf(channelType).ToolConfirmation, nil
}

// loadRunValues 读取运行能提供的可信上下文值：会话编号始终提供；服务客户的周期提供已核验客户的编号与邮箱；
// 只服务一位成员的运行提供该成员的编号与邮箱，即员工服务会话的提问员工、AI 单聊的成员与 Copilot 线程的创建者。
func loadRunValues(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (businesssystem.RunValues, error) {
	values := businesssystem.RunValues{domain.ContextValueConversationID: run.ConversationID}
	var member ServiceMember
	switch domain.AgentExecutionScopeKind(run.ScopeKind) {
	case domain.AgentExecutionScopeServiceSession:
		service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
		if err != nil {
			return nil, err
		}
		if domain.ServiceAudience(service.Audience) == domain.ServiceAudienceCustomer {
			customer, err := LoadServiceSessionCustomer(ctx, db, run.WorkspaceID, run.ScopeID)
			if err != nil {
				return nil, err
			}
			customer.addValues(values)
			return values, nil
		}
		if member, err = LoadServiceRequesterMember(ctx, db, run.WorkspaceID, run.ConversationID); err != nil {
			return nil, err
		}
	default:
		identity := db.NewSelect().TableExpr("conversations AS cv").
			ColumnExpr("COALESCE(ac.user_identity_id, sct.created_by_identity_id)").
			Join("LEFT JOIN agent_conversations AS ac ON ac.workspace_id = cv.workspace_id AND ac.conversation_id = cv.id").
			Join("LEFT JOIN service_copilot_threads AS sct ON sct.workspace_id = cv.workspace_id AND sct.conversation_id = cv.id").
			Where("cv.workspace_id = ? AND cv.id = ?", run.WorkspaceID, run.ConversationID).
			Where("cv.type IN (?)", bun.List([]domain.ConversationType{domain.ConversationTypeAgent, domain.ConversationTypeCopilot}))
		var err error
		if member, err = loadActiveMember(ctx, db, run.WorkspaceID, identity); err != nil {
			return nil, err
		}
	}
	member.addValues(values)
	return values, nil
}

// ServiceMember 表示运行所服务的在职成员，没有这样的成员时用户编号为空。
type ServiceMember struct {
	UserID string
	Email  string
}

// addValues 把成员的编号与邮箱写入可信上下文值，用户编号为空时不写入。
func (m ServiceMember) addValues(values businesssystem.RunValues) {
	if m.UserID != "" {
		values[domain.ContextValueMemberUserID], values[domain.ContextValueMemberEmail] = m.UserID, m.Email
	}
}

// LoadServiceRequesterMember 读取员工服务会话中提问员工对应的在职成员。
func LoadServiceRequesterMember(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (ServiceMember, error) {
	identity := db.NewSelect().TableExpr("service_conversations AS svc").ColumnExpr("cs.source_id").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = svc.workspace_id AND cs.id = svc.requester_subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", workspaceID, conversationID)
	return loadActiveMember(ctx, db, workspaceID, identity)
}

// loadActiveMember 读取身份子查询给出的企业身份对应的在职成员及其账号邮箱。
func loadActiveMember(ctx context.Context, db bun.IDB, workspaceID string, identity *bun.SelectQuery) (ServiceMember, error) {
	member := ServiceMember{}
	err := db.NewSelect().TableExpr("users AS u").
		ColumnExpr("u.id AS user_id, acc.email").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Where("u.workspace_id = ? AND u.identity_id = (?)", workspaceID, identity).
		Where("u.status = ?", domain.IdentityStatusActive).
		Scan(ctx, &member.UserID, &member.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceMember{}, nil
	}
	if err != nil {
		return ServiceMember{}, fmt.Errorf("load served member: %w", err)
	}
	return member, nil
}

// ServiceSessionCustomer 表示渠道来源客服周期的已验证客户，未验证时企业用户编号为空。
type ServiceSessionCustomer struct {
	UserID string
	Email  string
}

// addValues 把已核验客户的编号与邮箱写入可信上下文值，客户未核验时不写入。
func (c ServiceSessionCustomer) addValues(values businesssystem.RunValues) {
	if c.UserID == "" {
		return
	}
	values[domain.ContextValueCustomerUserID] = c.UserID
	if c.Email != "" {
		values[domain.ContextValueCustomerEmail] = c.Email
	}
}

// LoadServiceSessionCustomer 读取渠道来源客服周期的渠道身份对应的已验证客户与其主要邮箱，判定与客户上下文消息一致。
func LoadServiceSessionCustomer(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID string) (ServiceSessionCustomer, error) {
	row := struct {
		VerifiedUserID *string `bun:"verified_user_id"`
		Email          *string `bun:"email"`
	}{}
	if err := db.NewSelect().
		TableExpr("service_sessions AS ss").
		ColumnExpr("ci.verified_user_id").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.workspace_id = c.workspace_id AND cm.contact_id = c.id AND cm.type = ? AND cm.is_primary) AS email", domain.ContactMethodTypeEmail).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = ss.workspace_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("JOIN contacts AS c ON c.id = ci.contact_id AND c.workspace_id = ci.workspace_id").
		Where("ss.workspace_id = ?", workspaceID).
		Where("ss.id = ?", serviceSessionID).
		Scan(ctx, &row); err != nil {
		return ServiceSessionCustomer{}, fmt.Errorf("load service session customer: %w", err)
	}
	customer := ServiceSessionCustomer{}
	if row.VerifiedUserID != nil {
		customer.UserID, customer.Email = *row.VerifiedUserID, support.Deref(row.Email)
	}
	return customer, nil
}
