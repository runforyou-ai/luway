//go:build server

package identity

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const activeAgentRevisionSchemaVersion = 1

// ErrServiceHandlingRequired 表示当前身份未开启处理服务请求。
var ErrServiceHandlingRequired = errors.New("service handling identity required")

// LockActiveServiceHandlingUser 锁定当前真人身份的有效账号，并校验其已开启处理服务请求；未开启时返回 ErrServiceHandlingRequired。
func LockActiveServiceHandlingUser(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	if err := LockActiveUser(ctx, tx, identity); err != nil {
		return err
	}
	var handlesServiceRequests bool
	if err := tx.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
		Column("oi.handles_service_requests").
		Where("oi.organization_id = ? AND oi.id = ?", identity.Organization.ID, identity.OrganizationIdentity.ID).
		Scan(ctx, &handlesServiceRequests); err != nil {
		return err
	}
	if !handlesServiceRequests {
		return ErrServiceHandlingRequired
	}
	return nil
}

// ListActiveServiceHandlingIdentities 返回有效的接待身份。
func ListActiveServiceHandlingIdentities(ctx context.Context, db bun.IDB, organizationID string) ([]servermodels.OrganizationIdentity, error) {
	identities := make([]servermodels.OrganizationIdentity, 0)
	err := serviceHandlingIdentityQuery(db, &identities, organizationID).
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Scan(ctx)
	return identities, err
}

// LoadActiveServiceHandlingIdentity 返回指定的有效接待身份。
func LoadActiveServiceHandlingIdentity(ctx context.Context, db bun.IDB, organizationID, identityID string) (*servermodels.OrganizationIdentity, error) {
	identity := &servermodels.OrganizationIdentity{}
	err := serviceHandlingIdentityQuery(db, identity, organizationID).
		Where("oi.id = ?", identityID).
		Scan(ctx)
	return identity, err
}

// LockActiveServiceHandlingIdentity 对指定身份取 FOR KEY SHARE，再以锁后的语句快照返回有效接待身份，锁等待期间提交的停用或关闭接待随之生效。
func LockActiveServiceHandlingIdentity(ctx context.Context, db bun.IDB, organizationID, identityID string) (*servermodels.OrganizationIdentity, error) {
	var lockedID string
	if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
		Column("oi.id").
		Where("oi.organization_id = ? AND oi.id = ?", organizationID, identityID).
		For("KEY SHARE").
		Scan(ctx, &lockedID); err != nil {
		return &servermodels.OrganizationIdentity{}, err
	}
	return LoadActiveServiceHandlingIdentity(ctx, db, organizationID, identityID)
}

// ApplyServiceHandlingConditions 给以 oi 为别名的企业身份查询追加有效接待身份条件：真人成员开启接待且账号有效；AI 员工服务对象包含客户、账号有效，并使用托管执行与当前 Revision Schema 版本。
func ApplyServiceHandlingConditions(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		Where(`((oi.type = ? AND oi.handles_service_requests AND EXISTS (
				SELECT 1 FROM users AS hu
				WHERE hu.identity_id = oi.id AND hu.organization_id = oi.organization_id AND hu.status = ?))
			OR (oi.type = ? AND EXISTS (
				SELECT 1 FROM agents AS ha
				JOIN agent_revisions AS har ON har.id = ha.active_revision_id AND har.agent_id = ha.id AND har.organization_id = ha.organization_id
				WHERE ha.identity_id = oi.id AND ha.organization_id = oi.organization_id AND ha.status = ?
					AND ? = ANY(ha.service_audiences)
					AND har.execution_mode = ? AND har.schema_version = ?)))`,
			domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive,
			domain.OrganizationIdentityTypeAgent, domain.IdentityStatusActive, domain.ServiceAudienceCustomer,
			domain.AgentExecutionModeManaged, activeAgentRevisionSchemaVersion,
		)
}

// ApplyDirectServiceAgentConditions 给以 oi 为别名的企业身份查询追加单聊服务会话的 AI 员工条件：是该 AI 聊天的 AI 员工，账号有效、服务对象包含员工，并使用托管执行与当前 Revision Schema 版本。
func ApplyDirectServiceAgentConditions(query *bun.SelectQuery, conversationID string) *bun.SelectQuery {
	return query.
		Where(`oi.type = ? AND EXISTS (
				SELECT 1 FROM agents AS da
				JOIN agent_revisions AS dar ON dar.id = da.active_revision_id AND dar.agent_id = da.id AND dar.organization_id = da.organization_id
				JOIN agent_conversations AS dac ON dac.organization_id = da.organization_id AND dac.agent_identity_id = da.identity_id AND dac.conversation_id = ?
				WHERE da.identity_id = oi.id AND da.organization_id = oi.organization_id AND da.status = ?
					AND ? = ANY(da.service_audiences)
					AND dar.execution_mode = ? AND dar.schema_version = ?)`,
			domain.OrganizationIdentityTypeAgent, conversationID, domain.IdentityStatusActive, domain.ServiceAudienceEmployee,
			domain.AgentExecutionModeManaged, activeAgentRevisionSchemaVersion,
		)
}

// LockServiceHandlingIdentity 对指定身份取 FOR KEY SHARE，再返回可承接单聊服务会话的身份：开启接待的有效真人成员，或该会话满足服务条件的 AI 员工。
func LockServiceHandlingIdentity(ctx context.Context, db bun.IDB, organizationID, conversationID, identityID string) (*servermodels.OrganizationIdentity, error) {
	var identityType string
	if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
		Column("oi.type").
		Where("oi.organization_id = ? AND oi.id = ?", organizationID, identityID).
		For("KEY SHARE").
		Scan(ctx, &identityType); err != nil {
		return &servermodels.OrganizationIdentity{}, err
	}
	identity := &servermodels.OrganizationIdentity{}
	query := db.NewSelect().Model(identity).
		Column("oi.id", "oi.organization_id", "oi.type", "oi.display_name", "oi.avatar_file_id", "oi.work_status").
		Where("oi.organization_id = ? AND oi.id = ?", organizationID, identityID)
	if domain.OrganizationIdentityType(identityType) == domain.OrganizationIdentityTypeAgent {
		query = ApplyDirectServiceAgentConditions(query, conversationID)
	} else {
		query = ApplyServiceHandlingConditions(query).Where("oi.type = ?", domain.OrganizationIdentityTypeUser)
	}
	err := query.Scan(ctx)
	return identity, err
}

// serviceHandlingIdentityQuery 构造统一的有效接待身份查询。
func serviceHandlingIdentityQuery(db bun.IDB, model any, organizationID string) *bun.SelectQuery {
	return ApplyServiceHandlingConditions(db.NewSelect().Model(model).
		Column("oi.id", "oi.organization_id", "oi.type", "oi.display_name", "oi.avatar_file_id", "oi.work_status").
		Where("oi.organization_id = ?", organizationID))
}

// TeamServiceHandlerQuery 构造团队内开启接待真人成员的存在性查询，调用方以 oi 与 tm 别名追加企业和团队条件。
func TeamServiceHandlerQuery(db bun.IDB) *bun.SelectQuery {
	return ApplyServiceHandlingConditions(db.NewSelect().
		TableExpr("organization_identities AS oi").ColumnExpr("1").
		Join("JOIN team_members AS tm ON tm.organization_id = oi.organization_id AND tm.identity_id = oi.id").
		Where("oi.type = ?", domain.OrganizationIdentityTypeUser))
}

// TeamHasServiceHandler 判断团队内是否存在开启接待的有效真人成员。
func TeamHasServiceHandler(ctx context.Context, db bun.IDB, organizationID, teamID string) (bool, error) {
	return TeamServiceHandlerQuery(db).
		Where("oi.organization_id = ? AND tm.team_id = ?", organizationID, teamID).
		Exists(ctx)
}

// ApplyActiveMemberConditions 给以 oi 为别名的企业身份查询追加有效成员条件：真人成员账号有效或 AI 员工有效。
func ApplyActiveMemberConditions(query *bun.SelectQuery) *bun.SelectQuery {
	return query.Where(`((oi.type = ? AND EXISTS (
			SELECT 1 FROM users AS mu WHERE mu.identity_id = oi.id AND mu.organization_id = oi.organization_id AND mu.status = ?))
		OR (oi.type = ? AND EXISTS (
			SELECT 1 FROM agents AS ma WHERE ma.identity_id = oi.id AND ma.organization_id = oi.organization_id AND ma.status = ?)))`,
		domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive, domain.OrganizationIdentityTypeAgent, domain.IdentityStatusActive)
}
