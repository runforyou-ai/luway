//go:build server

package member

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrQueryInvalid 表示成员选择项或同事目录的分页条件无效。
var ErrQueryInvalid = errors.New("member query invalid")

// ListColleaguesInput 定义通讯录同事目录查询条件。
type ListColleaguesInput struct {
	Query    string
	Page     int
	PageSize int
}

// Colleague 定义同事目录项：在职成员，或服务对象包含本企业员工的在职 AI 员工（服务台）。
type Colleague struct {
	IdentityID      string                          `bun:"identity_id"`
	IdentityType    domain.OrganizationIdentityType `bun:"identity_type"`
	UserID          string                          `bun:"user_id"`
	AgentID         string                          `bun:"agent_id"`
	DisplayName     string                          `bun:"display_name"`
	AvatarFileID    *string                         `bun:"avatar_file_id"`
	WorkStatus      domain.WorkStatus               `bun:"work_status"`
	Email           string                          `bun:"email"`
	ResponsibleName string                          `bun:"responsible_name"`
	Teams           []teamaction.Summary            `bun:"-"`
	CreatedAt       time.Time                       `bun:"created_at"`
}

// ListColleaguesOutput 定义同事目录分页结果。
type ListColleaguesOutput struct {
	Colleagues []Colleague
	Page       common.PageInfo
}

// ListColleaguesQuery 读取通讯录同事目录。
type ListColleaguesQuery struct{ db *bun.DB }

// NewListColleaguesQuery 创建通讯录同事目录查询。
func NewListColleaguesQuery(db *bun.DB) *ListColleaguesQuery { return &ListColleaguesQuery{db: db} }

// Execute 返回服务台在前、成员在后并各自按名称排序的同事目录，服务台只带在职负责人的姓名。
func (q *ListColleaguesQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListColleaguesInput) (ListColleaguesOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return ListColleaguesOutput{}, ErrQueryInvalid
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where("oi.organization_id = ?", identity.Organization.ID).
			Where("((oi.type = ? AND u.status = ?) OR (oi.type = ? AND a.status = ? AND ? = ANY(a.service_audiences)))",
				domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive,
				domain.OrganizationIdentityTypeAgent, domain.IdentityStatusActive, domain.ServiceAudienceEmployee)
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
	total, err := apply(base()).Count(ctx)
	if err != nil {
		return ListColleaguesOutput{}, fmt.Errorf("count colleagues: %w", err)
	}
	colleagues := make([]Colleague, 0)
	if err := apply(base()).
		ColumnExpr("oi.id::text AS identity_id, oi.type AS identity_type, COALESCE(u.id::text, '') AS user_id, COALESCE(a.id::text, '') AS agent_id").
		ColumnExpr("oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.work_status, COALESCE(acc.email, '') AS email, COALESCE(roi.display_name, '') AS responsible_name, oi.created_at").
		Join("LEFT JOIN users AS ru ON ru.id = a.responsible_user_id AND ru.organization_id = a.organization_id AND ru.status = ?", domain.IdentityStatusActive).
		Join("LEFT JOIN organization_identities AS roi ON roi.id = ru.identity_id AND roi.organization_id = ru.organization_id").
		OrderExpr("oi.type = ? DESC, lower(oi.display_name) ASC, oi.id ASC", domain.OrganizationIdentityTypeAgent).
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &colleagues); err != nil {
		return ListColleaguesOutput{}, fmt.Errorf("list colleagues: %w", err)
	}
	identityIDs := make([]string, 0, len(colleagues))
	for _, colleague := range colleagues {
		identityIDs = append(identityIDs, colleague.IdentityID)
	}
	teamsByIdentity, err := teamaction.LoadTeamsByIdentity(ctx, q.db, identity.Organization.ID, identityIDs)
	if err != nil {
		return ListColleaguesOutput{}, fmt.Errorf("load colleague teams: %w", err)
	}
	for index := range colleagues {
		colleagues[index].Teams = teamsByIdentity[colleagues[index].IdentityID]
	}
	return ListColleaguesOutput{Colleagues: colleagues, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
