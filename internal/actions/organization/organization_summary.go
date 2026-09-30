//go:build server

package organization

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ErrNotFound 表示目标工作区不存在。
var ErrNotFound = errors.New("organization not found")

// Entitlement 描述工作区当前生效的服务权益快照。
type Entitlement struct {
	Revision      int64
	PlanCode      string
	ServiceEndsAt *time.Time
	AppliedAt     time.Time
}

// Summary 描述运营侧查看的工作区摘要；Entitlement 只在工作区有权益快照时存在。
type Summary struct {
	ID              string
	Name            string
	Slug            string
	LifecycleStatus domain.OrganizationLifecycleStatus
	Entitlement     *Entitlement
	CreatedAt       time.Time
}

// ListSummariesInput 定义运营工作区列表的筛选与分页条件，LifecycleStatus 为空表示不按状态筛选。
type ListSummariesInput struct {
	Query           string
	LifecycleStatus domain.OrganizationLifecycleStatus
	Page            int
	PageSize        int
}

// SummaryList 是一页工作区摘要及符合条件的总数。
type SummaryList struct {
	Items []Summary
	Total int
}

// summaryRow 是工作区摘要查询的扫描行。
type summaryRow struct {
	ID              string
	Name            string
	Slug            string
	LifecycleStatus string
	Revision        *int64
	PlanCode        *string
	ServiceEndsAt   *time.Time
	AppliedAt       *time.Time
	CreatedAt       time.Time
}

// SummaryQuery 跨工作区读取运营侧工作区摘要。
type SummaryQuery struct {
	db *bun.DB
}

// NewSummaryQuery 创建运营侧工作区摘要查询。
func NewSummaryQuery(db *bun.DB) *SummaryQuery {
	return &SummaryQuery{db: db}
}

// Get 返回指定工作区的摘要，工作区不存在时返回 ErrNotFound。
func (q *SummaryQuery) Get(ctx context.Context, organizationID string) (Summary, error) {
	organizationID, ok := common.NormalizeUUID(strings.TrimSpace(organizationID))
	if !ok {
		return Summary{}, ErrNotFound
	}
	var row summaryRow
	err := q.selectSummaries().Where("o.id = ?", organizationID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	if err != nil {
		return Summary{}, err
	}
	return row.summary(), nil
}

// List 按名称或标识关键字和生命周期状态分页返回工作区摘要，按创建时间倒序。
func (q *SummaryQuery) List(ctx context.Context, input ListSummariesInput) (SummaryList, error) {
	query := q.selectSummaries()
	if keyword := strings.TrimSpace(input.Query); keyword != "" {
		pattern := common.ContainsPattern(keyword)
		query = query.Where("(o.name ILIKE ? OR o.slug ILIKE ?)", pattern, pattern)
	}
	if input.LifecycleStatus != "" {
		query = query.Where("o.lifecycle_status = ?", string(input.LifecycleStatus))
	}
	var rows []summaryRow
	total, err := query.OrderExpr("o.created_at DESC, o.id DESC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		ScanAndCount(ctx, &rows)
	if err != nil {
		return SummaryList{}, err
	}
	items := make([]Summary, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.summary())
	}
	return SummaryList{Items: items, Total: total}, nil
}

// selectSummaries 构造关联权益快照的工作区摘要查询。
func (q *SummaryQuery) selectSummaries() *bun.SelectQuery {
	return q.db.NewSelect().
		TableExpr("organizations AS o").
		Join("LEFT JOIN organization_entitlements AS oe ON oe.organization_id = o.id").
		ColumnExpr("o.id::text, o.name, o.slug, o.lifecycle_status, o.created_at").
		ColumnExpr("oe.revision, oe.plan_code, oe.service_ends_at, oe.applied_at")
}

// summary 把扫描行转换为工作区摘要。
func (row summaryRow) summary() Summary {
	summary := Summary{
		ID:              row.ID,
		Name:            row.Name,
		Slug:            row.Slug,
		LifecycleStatus: domain.OrganizationLifecycleStatus(row.LifecycleStatus),
		CreatedAt:       row.CreatedAt,
	}
	// 工作区有权益快照时组装权益摘要。
	if row.Revision != nil && row.PlanCode != nil && row.AppliedAt != nil {
		summary.Entitlement = &Entitlement{
			Revision:      *row.Revision,
			PlanCode:      *row.PlanCode,
			ServiceEndsAt: row.ServiceEndsAt,
			AppliedAt:     *row.AppliedAt,
		}
	}
	return summary
}
