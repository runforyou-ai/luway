//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/uptrace/bun"
)

// serviceAgentCondition 限定 agents 记录为服务客户或员工的 AI 员工，调用方查询的 agents 别名为 a。
var serviceAgentCondition = "NOT " + personalAgentCondition

// loadAgent 读取当前企业中服务客户或员工的 AI 员工详情。
func loadAgent(ctx context.Context, db bun.IDB, workspaceID, agentID string) (*Agent, error) {
	agent := &Agent{}
	err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS id, a.identity_id::text AS identity_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, a.service_audiences, a.handoff_team_id::text AS handoff_team_id, a.status, oi.work_status, oi.created_at").
		Join("JOIN workspace_identities AS oi ON oi.id = a.identity_id AND oi.workspace_id = a.workspace_id").
		Where("a.id = ?", agentID).
		Where(serviceAgentCondition).
		Where("a.workspace_id = ?", workspaceID).
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
		Join("JOIN users AS u ON u.id = a.responsible_user_id AND u.workspace_id = a.workspace_id").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Join("JOIN workspace_identities AS oi ON oi.id = u.identity_id AND oi.workspace_id = u.workspace_id").
		Where("a.id = ? AND a.workspace_id = ?", agentID, workspaceID).
		Scan(ctx, responsible)
	if err == nil {
		agent.Responsible = responsible
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// 读取使用的工作区电脑，电脑撤销时绑定已在同一事务中解除。
	computer := &Computer{}
	err = db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("cmp.id::text AS computer_id, cmp.name AS computer_name, COALESCE(a.computer_grant, '{}'::jsonb) AS computer_grant, a.local_agents").
		Join("JOIN computers AS cmp ON cmp.id = a.computer_id AND cmp.workspace_id = a.workspace_id").
		Where("a.id = ? AND a.workspace_id = ?", agentID, workspaceID).
		Scan(ctx, computer)
	if err == nil {
		agent.Computer = computer
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	agent.Teams, err = teamaction.LoadIdentityTeams(ctx, db, workspaceID, agent.IdentityID)
	if err != nil {
		return nil, err
	}
	agent.Execution, err = loadAgentExecution(ctx, db, workspaceID, agentID)
	return agent, err
}
