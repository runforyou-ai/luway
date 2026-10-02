//go:build server

package agent

import (
	"context"
	"fmt"
	"strings"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListAgentsQuery 读取企业 AI 员工目录。
type ListAgentsQuery struct{ db *bun.DB }

// NewListAgentsQuery 创建 AI 员工目录查询。
func NewListAgentsQuery(db *bun.DB) *ListAgentsQuery {
	return &ListAgentsQuery{db: db}
}

// Execute 返回满足条件的服务型 AI 员工与当前成员负责的个人 AI 员工分页列表。
func (q *ListAgentsQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListInput) (ListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	input.Status = domain.IdentityStatus(strings.TrimSpace(string(input.Status)))
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid || (input.Status != "" && input.Status != domain.IdentityStatusActive && input.Status != domain.IdentityStatusInactive) {
		return ListOutput{}, ErrQueryInvalid
	}
	applyFilters := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.
			Where("a.organization_id = ?", identity.Organization.ID).
			Where("(? OR a.responsible_user_id = ?)", bun.Safe(serviceAgentCondition), identity.User.ID)
		if input.Status != "" {
			query = query.Where("a.status = ?", input.Status)
		}
		if input.Query != "" {
			query = query.Where("oi.display_name ILIKE ?", common.ContainsPattern(input.Query))
		}
		return query
	}
	base := func() *bun.SelectQuery {
		return q.db.NewSelect().TableExpr("agents AS a").
			Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id").
			Join("LEFT JOIN devices AS d ON d.id = a.device_id AND d.organization_id = a.organization_id")
	}
	total, err := applyFilters(base()).Count(ctx)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count agents: %w", err)
	}
	agents := make([]ListItem, 0)
	if err := applyFilters(base()).
		ColumnExpr("a.id::text AS id, a.identity_id::text AS identity_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, a.service_audiences, a.status, oi.work_status, oi.created_at").
		ColumnExpr("a.paused_at, a.device_id::text AS device_id, d.name AS device_name, d.revoked_at AS device_revoked_at, d.last_seen_at AS device_last_seen_at").
		OrderExpr("lower(oi.display_name) ASC, a.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &agents); err != nil {
		return ListOutput{}, fmt.Errorf("list agents: %w", err)
	}
	agentIDs := make([]string, 0, len(agents))
	identityIDs := make([]string, 0, len(agents))
	for _, agent := range agents {
		agentIDs = append(agentIDs, agent.ID)
		identityIDs = append(identityIDs, agent.IdentityID)
	}
	executions, err := loadAgentExecutionSummaries(ctx, q.db, identity.Organization.ID, agentIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("load agent execution summaries: %w", err)
	}
	teamsByIdentity, err := teamaction.LoadTeamsByIdentity(ctx, q.db, identity.Organization.ID, identityIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("load agent teams: %w", err)
	}
	for index := range agents {
		agents[index].Teams = teamsByIdentity[agents[index].IdentityID]
		agents[index].Execution = executions[agents[index].ID]
	}
	return ListOutput{Agents: agents, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
