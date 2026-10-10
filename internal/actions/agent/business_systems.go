//go:build server

package agent

import (
	"context"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// BusinessSystemOption 定义 AI 员工授权使用的业务系统选项。
type BusinessSystemOption struct {
	ID        string
	Name      string
	ToolCount int
}

// ListBusinessSystemOptionsQuery 读取当前工作区的业务系统目录摘要。
type ListBusinessSystemOptionsQuery struct{ db *bun.DB }

// NewListBusinessSystemOptionsQuery 创建业务系统选项查询。
func NewListBusinessSystemOptionsQuery(db *bun.DB) *ListBusinessSystemOptionsQuery {
	return &ListBusinessSystemOptionsQuery{db: db}
}

// Execute 返回全部已配置业务系统，不探测远端连接或工具可用性。
func (q *ListBusinessSystemOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]BusinessSystemOption, error) {
	options := make([]BusinessSystemOption, 0)
	err := q.db.NewSelect().TableExpr("business_systems AS bs").
		ColumnExpr("bs.id, bs.name, jsonb_array_length(bs.tools) AS tool_count").
		Where("bs.workspace_id = ?", identity.Workspace.ID).
		OrderExpr("lower(bs.name) ASC, bs.id ASC").Scan(ctx, &options)
	return options, err
}

// validateAndLockBusinessSystems 规范化、校验并锁定授权的业务系统，按业务系统编号排序；最高级别低于 L2 时不保留 L2 确认。
func validateAndLockBusinessSystems(ctx context.Context, tx bun.Tx, workspaceID string, grants []domain.BusinessSystemGrant) ([]domain.BusinessSystemGrant, error) {
	invalid := &common.FieldError{Fields: map[string]common.FieldCode{"businessSystems": ValidationBusinessSystemInvalid}}
	normalized := make([]domain.BusinessSystemGrant, 0, len(grants))
	for _, grant := range grants {
		id, valid := str.NormalizeUUID(grant.BusinessSystemID)
		if !valid || !grant.MaxLevel.Valid() {
			return nil, invalid
		}
		grant.BusinessSystemID = id
		grant.ConfirmL2 = grant.ConfirmL2 && domain.OperationLevelL2.AtMost(grant.MaxLevel)
		normalized = append(normalized, grant)
	}
	slices.SortFunc(normalized, func(left, right domain.BusinessSystemGrant) int {
		return strings.Compare(left.BusinessSystemID, right.BusinessSystemID)
	})
	ids := arr.Map(normalized, func(grant domain.BusinessSystemGrant) string { return grant.BusinessSystemID })
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, invalid
	}
	if len(ids) > 0 {
		var found []string
		if err := tx.NewSelect().Model((*servermodels.BusinessSystem)(nil)).Column("id").
			Where("bs.workspace_id = ?", workspaceID).Where("bs.id IN (?)", bun.List(ids)).
			OrderExpr("bs.id ASC").For("KEY SHARE").Scan(ctx, &found); err != nil {
			return nil, err
		}
		if len(found) != len(ids) {
			return nil, invalid
		}
	}
	return normalized, nil
}
