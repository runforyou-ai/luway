//go:build server

package team

import (
	"context"
	"fmt"
	"strings"
	"time"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// MemberListInput 定义团队成员列表查询条件。
type MemberListInput struct {
	Query      string
	WorkStatus domain.WorkStatus
	Page       int
	PageSize   int
}

// Member 定义团队成员信息。UserID 与 AgentID 按身份类型二选一，另一个为空。
type Member struct {
	IdentityID   string                          `bun:"identity_id"`
	IdentityType domain.OrganizationIdentityType `bun:"identity_type"`
	UserID       *string                         `bun:"user_id"`
	AgentID      *string                         `bun:"agent_id"`
	DisplayName  string                          `bun:"display_name"`
	AvatarFileID *string                         `bun:"avatar_file_id"`
	WorkStatus   domain.WorkStatus               `bun:"work_status"`
	JoinedAt     time.Time                       `bun:"joined_at"`
}

// MemberListOutput 定义团队成员分页结果。
type MemberListOutput struct {
	Members []Member
	Page    PageInfo
}

// ListMembersQuery 读取团队成员关系。
type ListMembersQuery struct{ db *bun.DB }

// NewListMembersQuery 创建团队成员列表查询。
func NewListMembersQuery(db *bun.DB) *ListMembersQuery {
	return &ListMembersQuery{db: db}
}

// Execute 返回团队成员分页列表。
func (q *ListMembersQuery) Execute(ctx context.Context, identity *servermodels.Identity, teamID string, input MemberListInput) (MemberListOutput, error) {
	input, err := normalizeMemberListInput(input)
	if err != nil {
		return MemberListOutput{}, err
	}
	if err := requireTeam(ctx, q.db, identity.Organization.ID, teamID); err != nil {
		return MemberListOutput{}, err
	}
	return q.list(ctx, identity, teamID, input)
}

// normalizeMemberListInput 规范化并校验团队成员列表查询条件。
func normalizeMemberListInput(input MemberListInput) (MemberListInput, error) {
	input.Query = strings.TrimSpace(input.Query)
	input.WorkStatus = domain.WorkStatus(strings.TrimSpace(string(input.WorkStatus)))
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return input, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	if input.WorkStatus != "" && !input.WorkStatus.Valid() {
		return input, &common.FieldError{Fields: map[string]common.FieldCode{"workStatus": ValidationWorkStatusInvalid}}
	}
	return input, nil
}

// list 读取指定团队的成员分页列表。
func (q *ListMembersQuery) list(ctx context.Context, identity *servermodels.Identity, teamID string, input MemberListInput) (MemberListOutput, error) {
	applyFilters := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.
			Where("tm.organization_id = ?", identity.Organization.ID).
			Where("oi.type IN (?, ?)", domain.OrganizationIdentityTypeUser, domain.OrganizationIdentityTypeAgent).
			Apply(identityaction.ApplyActiveMemberConditions).
			Where("tm.team_id = ?", teamID)
		if input.WorkStatus != "" {
			query = query.Where("oi.work_status = ?", input.WorkStatus)
		}
		if input.Query != "" {
			query = query.Where("oi.display_name ILIKE ?", common.ContainsPattern(input.Query))
		}
		return query
	}
	base := func() *bun.SelectQuery {
		return q.db.NewSelect().TableExpr("team_members AS tm").
			Join("JOIN organization_identities AS oi ON oi.id = tm.identity_id AND oi.organization_id = tm.organization_id").
			Join("LEFT JOIN users AS u ON u.identity_id = oi.id AND u.organization_id = oi.organization_id").
			Join("LEFT JOIN agents AS a ON a.identity_id = oi.id AND a.organization_id = oi.organization_id")
	}
	var total int
	if err := applyFilters(base()).ColumnExpr("count(*)").Scan(ctx, &total); err != nil {
		return MemberListOutput{}, fmt.Errorf("count team members: %w", err)
	}
	members := make([]Member, 0)
	if err := applyFilters(base()).
		ColumnExpr("oi.id::text AS identity_id, oi.type AS identity_type, u.id::text AS user_id, a.id::text AS agent_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.work_status, tm.created_at AS joined_at").
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &members); err != nil {
		return MemberListOutput{}, fmt.Errorf("list team members: %w", err)
	}
	return MemberListOutput{Members: members, Page: PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
