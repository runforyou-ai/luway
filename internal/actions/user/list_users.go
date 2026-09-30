//go:build server

// Package user 实现企业成员领域的查询和操作。
package user

import (
	"context"
	"fmt"
	"strings"

	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListUsersQuery 读取当前企业的成员列表。
type ListUsersQuery struct {
	db *bun.DB
}

// NewListUsersQuery 创建企业成员列表查询。
func NewListUsersQuery(db *bun.DB) *ListUsersQuery {
	return &ListUsersQuery{db: db}
}

// Execute 返回满足条件的企业成员分页列表。
func (q *ListUsersQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListInput) (ListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	input.Status = domain.IdentityStatus(strings.TrimSpace(string(input.Status)))
	input.RoleID = strings.TrimSpace(input.RoleID)
	input.TeamID = strings.TrimSpace(input.TeamID)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid ||
		(input.Status != "" && input.Status != domain.IdentityStatusActive && input.Status != domain.IdentityStatusInactive) ||
		(input.RoleID != "" && !common.ValidUUID(input.RoleID)) ||
		(input.TeamID != "" && !common.ValidUUID(input.TeamID)) {
		return ListOutput{}, ErrQueryInvalid
	}

	applyFilters := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Join("JOIN accounts AS acc ON acc.id = u.account_id").Where("u.organization_id = ?", identity.Organization.ID)
		if input.Status != "" {
			query = query.Where("u.status = ?", input.Status)
		}
		if input.RoleID != "" {
			query = query.Where("u.role_id = ?", input.RoleID)
		}
		if input.TeamID != "" {
			query = query.Where("EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.organization_id = u.organization_id AND tm.identity_id = u.identity_id AND tm.team_id = ?)", input.TeamID)
		}
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
				return group.
					Where("oi.display_name ILIKE ?", pattern).
					WhereOr("acc.email ILIKE ?", pattern)
			})
		}
		return query
	}

	total, err := applyFilters(q.db.NewSelect().TableExpr("users AS u").Join("JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeUser)).Count(ctx)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count users: %w", err)
	}
	users := make([]User, 0)
	if err := applyFilters(q.db.NewSelect().TableExpr("users AS u")).
		ColumnExpr("u.id::text AS id, u.identity_id::text AS identity_id").
		ColumnExpr("acc.email, u.status, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.handles_service_requests, u.max_service_sessions, oi.work_status, oi.created_at").
		ColumnExpr("r.id::text AS role_id, r.kind AS role_kind, r.name AS role_name").
		Join("JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeUser).
		Join("JOIN roles AS r ON r.id = u.role_id AND r.organization_id = u.organization_id").
		OrderExpr("lower(oi.display_name) ASC, u.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &users); err != nil {
		return ListOutput{}, fmt.Errorf("list users: %w", err)
	}
	identityIDs := make([]string, 0, len(users))
	for _, user := range users {
		identityIDs = append(identityIDs, user.IdentityID)
	}
	teamsByIdentity, err := teamaction.LoadTeamsByIdentity(ctx, q.db, identity.Organization.ID, identityIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("load user teams: %w", err)
	}
	for index := range users {
		users[index].Teams = teamsByIdentity[users[index].IdentityID]
	}
	return ListOutput{Users: users, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
