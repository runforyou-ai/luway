//go:build server

package team

import (
	"context"
	"fmt"
	"maps"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AddMembersAction 将企业身份批量加入团队。
type AddMembersAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewAddMembersAction 创建团队成员添加操作。
func NewAddMembersAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *AddMembersAction {
	return &AddMembersAction{db: db, enqueuer: enqueuer}
}

// Execute 规范化并校验成员后批量建立团队关系，并为新加入的真人成员从团队队列补分配。
func (a *AddMembersAction) Execute(ctx context.Context, identity *servermodels.Identity, teamID string, members []MemberIdentity) (*TeamRecord, error) {
	var team *TeamRecord
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockTeam(ctx, tx, identity.Workspace.ID, teamID); err != nil {
			return err
		}
		uniqueIDs := make(map[string]domain.WorkspaceIdentityType, len(members))
		for _, member := range members {
			identityID, identityIDValid := str.NormalizeUUID(member.IdentityID)
			if (member.IdentityType != domain.WorkspaceIdentityTypeUser && member.IdentityType != domain.WorkspaceIdentityTypeAgent) || !identityIDValid {
				return ErrMemberInvalid
			}
			uniqueIDs[identityID] = member.IdentityType
		}
		if len(uniqueIDs) == 0 {
			return ErrMemberInvalid
		}
		ids := slices.Collect(maps.Keys(uniqueIDs))
		var storedIdentities []servermodels.WorkspaceIdentity
		err := tx.NewSelect().Model(&storedIdentities).
			ColumnExpr("oi.id, oi.type").
			Where("oi.workspace_id = ?", identity.Workspace.ID).
			Apply(identityaction.ApplyActiveMemberConditions).
			Where("oi.id IN (?)", bun.List(ids)).
			Scan(ctx)
		if err != nil {
			return err
		}
		if len(storedIdentities) != len(ids) {
			return ErrMemberInvalid
		}
		for _, storedIdentity := range storedIdentities {
			if uniqueIDs[storedIdentity.ID] != domain.WorkspaceIdentityType(storedIdentity.Type) {
				return ErrMemberInvalid
			}
		}
		relations := arr.Map(ids, func(id string) servermodels.TeamMember {
			return servermodels.TeamMember{WorkspaceID: identity.Workspace.ID, TeamID: teamID, IdentityID: id, CreatedByUserID: identity.User.ID}
		})
		var added []string
		if err := tx.NewInsert().Model(&relations).
			Column("workspace_id", "team_id", "identity_id", "created_by_user_id").
			On("CONFLICT (workspace_id, team_id, identity_id) DO NOTHING").
			Returning("identity_id").
			Scan(ctx, &added); err != nil {
			return err
		}
		// 新加入的成员改变队列在线情况，通知企业全部网站访客重新读取接待状态，并为新加入的真人成员补分配。
		if len(added) > 0 {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		}
		for _, id := range added {
			if uniqueIDs[id] != domain.WorkspaceIdentityTypeUser {
				continue
			}
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{WorkspaceID: identity.Workspace.ID, IdentityID: id}); err != nil {
				return err
			}
		}
		team, err = loadTeam(ctx, tx, identity.Workspace.ID, teamID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("add team members: %w", err)
	}
	return team, nil
}
