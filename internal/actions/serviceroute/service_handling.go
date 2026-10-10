//go:build server

package serviceroute

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// activeAgentRevisionSchemaVersion 是接待 AI 员工的生效配置版本须满足的 Revision Schema 版本。
const activeAgentRevisionSchemaVersion = 1

// ListActiveServiceHandlingIdentities 返回有效的接待身份，AI 员工须服务 audiences 中的任一对象。
func ListActiveServiceHandlingIdentities(ctx context.Context, db bun.IDB, workspaceID string, audiences ...domain.ServiceAudience) ([]servermodels.WorkspaceIdentity, error) {
	identities := make([]servermodels.WorkspaceIdentity, 0)
	err := serviceHandlingIdentityQuery(db, &identities, workspaceID, audiences).
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Scan(ctx)
	return identities, err
}

// LoadActiveServiceHandlingIdentity 返回指定的有效接待身份，AI 员工须服务 audience。
func LoadActiveServiceHandlingIdentity(ctx context.Context, db bun.IDB, workspaceID, identityID string, audience domain.ServiceAudience) (*servermodels.WorkspaceIdentity, error) {
	identity := &servermodels.WorkspaceIdentity{}
	err := serviceHandlingIdentityQuery(db, identity, workspaceID, []domain.ServiceAudience{audience}).
		Where("oi.id = ?", identityID).
		Scan(ctx)
	return identity, err
}

// LockActiveServiceHandlingIdentity 对指定身份取 FOR KEY SHARE，再以锁后的语句快照返回服务 audience 的有效接待身份，锁等待期间提交的停用或关闭接待随之生效。
func LockActiveServiceHandlingIdentity(ctx context.Context, db bun.IDB, workspaceID, identityID string, audience domain.ServiceAudience) (*servermodels.WorkspaceIdentity, error) {
	var lockedID string
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).
		Column("oi.id").
		Where("oi.workspace_id = ? AND oi.id = ?", workspaceID, identityID).
		For("KEY SHARE").
		Scan(ctx, &lockedID); err != nil {
		return &servermodels.WorkspaceIdentity{}, err
	}
	return LoadActiveServiceHandlingIdentity(ctx, db, workspaceID, identityID, audience)
}

// ApplyServiceHandlingConditions 给以 oi 为别名的企业身份查询追加有效接待身份条件：真人成员开启接待且账号有效；AI 员工服务 audiences 中的任一对象、账号有效，并使用托管执行与当前 Revision Schema 版本。
func ApplyServiceHandlingConditions(query *bun.SelectQuery, audiences []domain.ServiceAudience) *bun.SelectQuery {
	return query.
		Where(`((`+serviceHandlingUserCondition+`)
			OR (oi.type = ? AND EXISTS (
				SELECT 1 FROM agents AS ha
				JOIN agent_revisions AS har ON har.id = ha.active_revision_id AND har.agent_id = ha.id AND har.workspace_id = ha.workspace_id
				WHERE ha.identity_id = oi.id AND ha.workspace_id = oi.workspace_id AND ha.status = ?
					AND ha.service_audiences && ?::text[]
					AND har.execution_mode = ? AND har.schema_version = ?)))`,
			domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive,
			domain.WorkspaceIdentityTypeAgent, domain.IdentityStatusActive, pgdialect.Array(audiences),
			domain.AgentExecutionModeManaged, activeAgentRevisionSchemaVersion,
		)
}

// ApplyServiceHandlingUserConditions 给以 oi 为别名的企业身份查询追加开启接待且账号有效的真人成员条件。
func ApplyServiceHandlingUserConditions(query *bun.SelectQuery) *bun.SelectQuery {
	return query.Where(serviceHandlingUserCondition, domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive)
}

// serviceHandlingUserCondition 是开启接待且账号有效的真人成员条件，参数依次为真人身份类型与有效账号状态。
const serviceHandlingUserCondition = `oi.type = ? AND oi.handles_service_requests AND EXISTS (
				SELECT 1 FROM users AS hu
				WHERE hu.identity_id = oi.id AND hu.workspace_id = oi.workspace_id AND hu.status = ?)`

// ApplyDirectServiceAgentConditions 给以 oi 为别名的企业身份查询追加单聊服务会话的 AI 员工条件：是该 AI 聊天的 AI 员工，账号有效、服务对象包含员工，并使用托管执行与当前 Revision Schema 版本。
func ApplyDirectServiceAgentConditions(query *bun.SelectQuery, conversationID string) *bun.SelectQuery {
	return query.
		Where(`oi.type = ? AND EXISTS (
				SELECT 1 FROM agents AS da
				JOIN agent_revisions AS dar ON dar.id = da.active_revision_id AND dar.agent_id = da.id AND dar.workspace_id = da.workspace_id
				JOIN agent_conversations AS dac ON dac.workspace_id = da.workspace_id AND dac.agent_identity_id = da.identity_id AND dac.conversation_id = ?
				WHERE da.identity_id = oi.id AND da.workspace_id = oi.workspace_id AND da.status = ?
					AND ? = ANY(da.service_audiences)
					AND dar.execution_mode = ? AND dar.schema_version = ?)`,
			domain.WorkspaceIdentityTypeAgent, conversationID, domain.IdentityStatusActive, domain.ServiceAudienceEmployee,
			domain.AgentExecutionModeManaged, activeAgentRevisionSchemaVersion,
		)
}

// LockServiceHandlingIdentity 对指定身份取 FOR KEY SHARE，再返回可承接单聊服务会话的身份：开启接待的有效真人成员，或该会话满足服务条件的 AI 员工。
func LockServiceHandlingIdentity(ctx context.Context, db bun.IDB, workspaceID, conversationID, identityID string) (*servermodels.WorkspaceIdentity, error) {
	var identityType string
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).
		Column("oi.type").
		Where("oi.workspace_id = ? AND oi.id = ?", workspaceID, identityID).
		For("KEY SHARE").
		Scan(ctx, &identityType); err != nil {
		return &servermodels.WorkspaceIdentity{}, err
	}
	identity := &servermodels.WorkspaceIdentity{}
	query := db.NewSelect().Model(identity).
		Column("oi.id", "oi.workspace_id", "oi.type", "oi.display_name", "oi.avatar_file_id", "oi.work_status").
		Where("oi.workspace_id = ? AND oi.id = ?", workspaceID, identityID)
	if domain.WorkspaceIdentityType(identityType) == domain.WorkspaceIdentityTypeAgent {
		query = ApplyDirectServiceAgentConditions(query, conversationID)
	} else {
		query = ApplyServiceHandlingUserConditions(query)
	}
	err := query.Scan(ctx)
	return identity, err
}

// serviceHandlingIdentityQuery 构造统一的有效接待身份查询，AI 员工须服务 audiences 中的任一对象。
func serviceHandlingIdentityQuery(db bun.IDB, model any, workspaceID string, audiences []domain.ServiceAudience) *bun.SelectQuery {
	return ApplyServiceHandlingConditions(db.NewSelect().Model(model).
		Column("oi.id", "oi.workspace_id", "oi.type", "oi.display_name", "oi.avatar_file_id", "oi.work_status").
		Where("oi.workspace_id = ?", workspaceID), audiences)
}

// TeamServiceHandlerQuery 构造团队内开启接待真人成员的存在性查询，调用方以 oi 与 tm 别名追加企业和团队条件。
func TeamServiceHandlerQuery(db bun.IDB) *bun.SelectQuery {
	return ApplyServiceHandlingUserConditions(db.NewSelect().
		TableExpr("workspace_identities AS oi").ColumnExpr("1").
		Join("JOIN team_members AS tm ON tm.workspace_id = oi.workspace_id AND tm.identity_id = oi.id"))
}

// TeamHasServiceHandler 判断团队内是否存在开启接待的有效真人成员。
func TeamHasServiceHandler(ctx context.Context, db bun.IDB, workspaceID, teamID string) (bool, error) {
	return TeamServiceHandlerQuery(db).
		Where("oi.workspace_id = ? AND tm.team_id = ?", workspaceID, teamID).
		Exists(ctx)
}
