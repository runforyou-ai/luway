//go:build server

package computer

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ReportCapabilitiesAction 保存执行器上报的执行能力。
type ReportCapabilitiesAction struct {
	db *bun.DB
}

// NewReportCapabilitiesAction 创建执行能力上报操作。
func NewReportCapabilitiesAction(db *bun.DB) *ReportCapabilitiesAction {
	return &ReportCapabilitiesAction{db: db}
}

// Execute 保存电脑的执行能力、执行器版本与同时执行的操作上限，上限无效时使用默认值；MCP 服务、工具与技能按名称排序保存。
func (a *ReportCapabilitiesAction) Execute(ctx context.Context, computer Identity, input CapabilitiesInput) error {
	capabilities := input.Capabilities
	slices.SortFunc(capabilities.MCPServers, func(left, right domain.ComputerMCPServer) int { return strings.Compare(left.Name, right.Name) })
	for index := range capabilities.MCPServers {
		slices.SortFunc(capabilities.MCPServers[index].Tools, func(left, right domain.ComputerMCPTool) int { return strings.Compare(left.Name, right.Name) })
	}
	slices.SortFunc(capabilities.Skills, func(left, right domain.ComputerSkill) int { return strings.Compare(left.Name, right.Name) })
	concurrency := input.MaxConcurrency
	if concurrency <= 0 || concurrency > maxConcurrencyLimit {
		concurrency = defaultMaxConcurrency
	}
	if _, err := a.db.NewUpdate().Model((*servermodels.Computer)(nil)).
		Set("capabilities = ?", capabilities).
		Set("executor_version = ?", strings.TrimSpace(input.ExecutorVersion)).
		Set("max_concurrency = ?", concurrency).
		Set("updated_at = now()").
		Where("id = ? AND organization_id = ?", computer.ComputerID, computer.OrganizationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("report computer capabilities: %w", err)
	}
	return nil
}
