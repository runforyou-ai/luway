//go:build server

package team

import (
	"context"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListMemberCandidatesQuery 读取尚未加入团队的企业身份。
type ListMemberCandidatesQuery struct{ db *bun.DB }

// NewListMemberCandidatesQuery 创建团队成员候选查询。
func NewListMemberCandidatesQuery(db *bun.DB) *ListMemberCandidatesQuery {
	return &ListMemberCandidatesQuery{db: db}
}

// Execute 返回尚未加入指定团队的企业身份分页列表。
func (q *ListMemberCandidatesQuery) Execute(ctx context.Context, identity *servermodels.Identity, teamID string, input MemberCandidateInput) (MemberCandidateOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return MemberCandidateOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	if err := requireTeam(ctx, q.db, identity.Organization.ID, teamID); err != nil {
		return MemberCandidateOutput{}, err
	}

	applyFilters := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.
			Where("oi.organization_id = ?", identity.Organization.ID).
			Apply(identityaction.ApplyActiveMemberConditions).
			Where("NOT EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.organization_id = oi.organization_id AND tm.team_id = ? AND tm.identity_id = oi.id)", teamID)
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
				return group.Where("oi.display_name ILIKE ?", pattern).WhereOr("acc.email ILIKE ?", pattern)
			})
		}
		return query
	}

	base := func() *bun.SelectQuery {
		return q.db.NewSelect().TableExpr("organization_identities AS oi").
			Join("LEFT JOIN users AS u ON u.identity_id = oi.id AND u.organization_id = oi.organization_id").
			Join("LEFT JOIN accounts AS acc ON acc.id = u.account_id").
			Join("LEFT JOIN agents AS a ON a.identity_id = oi.id AND a.organization_id = oi.organization_id")
	}
	total, err := applyFilters(base()).Count(ctx)
	if err != nil {
		return MemberCandidateOutput{}, fmt.Errorf("count team member candidates: %w", err)
	}
	members := make([]MemberCandidate, 0)
	if err := applyFilters(base()).
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("oi.id::text AS identity_id, oi.display_name, oi.avatar_file_id::text").
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &members); err != nil {
		return MemberCandidateOutput{}, fmt.Errorf("list team member candidates: %w", err)
	}
	return MemberCandidateOutput{Members: members, Page: PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
