//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"

	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// employeeIdentityCondition 限定 agents 记录为 AI 员工，调用方查询的 agents 别名为 a。
var employeeIdentityCondition = "a.identity_id IN (SELECT oi.id FROM organization_identities AS oi WHERE oi.organization_id = a.organization_id AND oi.type = '" + string(domain.OrganizationIdentityTypeAgent) + "')"

// loadAgent 读取当前企业中的 AI 员工详情。
func loadAgent(ctx context.Context, db bun.IDB, organizationID, agentID string) (*Agent, error) {
	if !common.ValidUUID(agentID) {
		return nil, ErrNotFound
	}
	agent := &Agent{}
	err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS id, a.identity_id::text AS identity_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, a.service_audiences, a.handoff_team_id::text AS handoff_team_id, a.status, oi.work_status, oi.created_at").
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeAgent).
		Where("a.id = ?", agentID).
		Where("a.organization_id = ?", organizationID).
		Scan(ctx, agent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// 读取负责人资料与账号状态，负责人停用后仍按原记录返回。
	responsible := &Responsible{}
	err = db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("u.id::text AS user_id, oi.display_name, acc.email, u.status").
		Join("JOIN users AS u ON u.id = a.responsible_user_id AND u.organization_id = a.organization_id").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Join("JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id").
		Where("a.id = ? AND a.organization_id = ?", agentID, organizationID).
		Scan(ctx, responsible)
	if err == nil {
		agent.Responsible = responsible
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	agent.Teams, err = teamaction.LoadIdentityTeams(ctx, db, organizationID, agent.IdentityID)
	if err != nil {
		return nil, err
	}
	agent.Execution, err = loadAgentExecution(ctx, db, organizationID, agentID)
	return agent, err
}
