//go:build server

package device

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ReportLocalAgentsAction 保存设备上报的可用本机 Agent。
type ReportLocalAgentsAction struct {
	db *bun.DB
}

// NewReportLocalAgentsAction 创建本机 Agent 上报操作。
func NewReportLocalAgentsAction(db *bun.DB) *ReportLocalAgentsAction {
	return &ReportLocalAgentsAction{db: db}
}

// Execute 用上报结果整体替换当前用户未撤销设备的可用本机 Agent，未知种类不保存。
func (a *ReportLocalAgentsAction) Execute(ctx context.Context, identity *servermodels.Identity, deviceID string, kinds []domain.LocalAgentKind) error {
	// 只保留受支持的种类并按集合保存。
	localAgents := make([]domain.LocalAgentKind, 0, len(kinds))
	for _, kind := range kinds {
		if domain.LocalAgentKindValid(kind) {
			localAgents = append(localAgents, kind)
		}
	}
	slices.Sort(localAgents)
	localAgents = slices.Compact(localAgents)
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.Device)(nil)).
			Set("local_agents = ?", localAgents).
			Set("updated_at = now()").
			Where("organization_id = ? AND user_id = ? AND id = ? AND revoked_at IS NULL", identity.Organization.ID, identity.User.ID, deviceID).
			Exec(ctx)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("report device local agents: %w", err)
	}
	slog.Info("设备可用本机 Agent 已更新", "organization_id", identity.Organization.ID, "device_id", deviceID, "local_agents", localAgents)
	return nil
}
