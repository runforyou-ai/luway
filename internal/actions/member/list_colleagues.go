//go:build server

package member

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
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
	IdentityID      string                       `bun:"identity_id"`
	IdentityType    domain.WorkspaceIdentityType `bun:"identity_type"`
	UserID          string                       `bun:"user_id"`
	AgentID         string                       `bun:"agent_id"`
	DisplayName     string                       `bun:"display_name"`
	AvatarFileID    *string                      `bun:"avatar_file_id"`
	WorkStatus      domain.WorkStatus            `bun:"work_status"`
	Email           string                       `bun:"email"`
	ResponsibleName string                       `bun:"responsible_name"`
	Teams           []teamaction.Summary         `bun:"-"`
	CreatedAt       time.Time                    `bun:"created_at"`
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
	colleagues := make([]Colleague, 0)
	total, err := identityDirectory(q.db, identity.Workspace.ID, input.Query).
		Where("((oi.type = ? AND u.status = ?) OR (oi.type = ? AND a.status = ? AND ? = ANY(a.service_audiences)))",
			domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive,
			domain.WorkspaceIdentityTypeAgent, domain.IdentityStatusActive, domain.ServiceAudienceEmployee).
		ColumnExpr("oi.id::text AS identity_id, oi.type AS identity_type, COALESCE(u.id::text, '') AS user_id, COALESCE(a.id::text, '') AS agent_id").
		ColumnExpr("oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.work_status, COALESCE(acc.email, '') AS email, COALESCE(roi.display_name, '') AS responsible_name, oi.created_at").
		Join("LEFT JOIN users AS ru ON ru.id = a.responsible_user_id AND ru.workspace_id = a.workspace_id AND ru.status = ?", domain.IdentityStatusActive).
		Join("LEFT JOIN workspace_identities AS roi ON roi.id = ru.identity_id AND roi.workspace_id = ru.workspace_id").
		OrderExpr("oi.type = ? DESC, lower(oi.display_name) ASC, oi.id ASC", domain.WorkspaceIdentityTypeAgent).
		Limit(int64(input.PageSize)).
		Offset(int64((input.Page-1)*input.PageSize)).
		ScanAndCount(ctx, &colleagues)
	if err != nil {
		return ListColleaguesOutput{}, fmt.Errorf("list colleagues: %w", err)
	}
	identityIDs := arr.Map(colleagues, func(colleague Colleague) string { return colleague.IdentityID })
	teamsByIdentity, err := teamaction.LoadTeamsByIdentity(ctx, q.db, identity.Workspace.ID, identityIDs)
	if err != nil {
		return ListColleaguesOutput{}, fmt.Errorf("load colleague teams: %w", err)
	}
	for index := range colleagues {
		colleagues[index].Teams = teamsByIdentity[colleagues[index].IdentityID]
	}
	return ListColleaguesOutput{Colleagues: colleagues, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: int(total)}}, nil
}
