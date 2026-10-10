//go:build server

package team

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// DeleteTeamAction 删除企业团队。
type DeleteTeamAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewDeleteTeamAction 创建团队删除操作。
func NewDeleteTeamAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *DeleteTeamAction {
	return &DeleteTeamAction{db: db, enqueuer: enqueuer}
}

// Execute 删除团队及其成员关系，清空渠道、咨询分类与 AI 员工转人工的团队关联，并把团队队列中的客服处理周期重置到公共队列，为并入公共队列的等待周期投递分配任务。
func (a *DeleteTeamAction) Execute(ctx context.Context, identity *servermodels.Identity, teamID string) error {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 删除团队会改变渠道路由，通知企业全部网站访客重新读取接待状态。
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 先锁定团队行，与转交给团队的共享锁互斥，队列清理后不会再有新的周期写入该团队。
		if err := lockTeam(ctx, tx, identity.Workspace.ID, teamID); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.TeamMember)(nil)).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("team_id = ?", teamID).
			Exec(ctx); err != nil {
			return err
		}
		if err := serviceroute.ResetChannelRoutingTarget(ctx, tx, identity.Workspace.ID, domain.ChannelRoutingTargetTypeTeam, teamID); err != nil {
			return err
		}
		if err := servicecategory.ClearTeam(ctx, tx, identity.Workspace.ID, teamID); err != nil {
			return err
		}
		// 清空 AI 员工指向该团队的转人工团队，转人工进入公共队列。
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("handoff_team_id = NULL").
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("handoff_team_id = ?", teamID).
			Exec(ctx); err != nil {
			return err
		}
		// 已关闭周期重开后仍读取队列，团队的全部客服处理周期并入公共队列；队列中的周期重新计算队列等待提醒。
		moved, err := servicestate.MoveTeamToPublicQueue(ctx, tx, identity.Workspace.ID, teamID)
		if err != nil {
			return err
		}
		assignments := arr.FilterMap(moved, func(session servermodels.ServiceSession) (serviceassignment.AssignInput, bool) {
			return serviceassignment.AssignInput{WorkspaceID: identity.Workspace.ID, ServiceSessionID: session.ID},
				domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen && session.AssigneeIdentityID == nil
		})
		if err := serviceassignment.EnqueueAssign(ctx, tx, a.enqueuer, assignments...); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*servermodels.Team)(nil)).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", teamID).
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	return nil
}
