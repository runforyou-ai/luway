//go:build server

package team

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/actions/serviceassignment"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
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
		if err := lockTeam(ctx, tx, identity.Organization.ID, teamID); err != nil {
			return err
		}
		uniqueIDs := make(map[string]domain.OrganizationIdentityType, len(members))
		for _, member := range members {
			identityID, identityIDValid := common.NormalizeUUID(member.IdentityID)
			if (member.IdentityType != domain.OrganizationIdentityTypeUser && member.IdentityType != domain.OrganizationIdentityTypeAgent) || !identityIDValid {
				return ErrMemberInvalid
			}
			uniqueIDs[identityID] = member.IdentityType
		}
		if len(uniqueIDs) == 0 {
			return ErrMemberInvalid
		}
		ids := make([]string, 0, len(uniqueIDs))
		for id := range uniqueIDs {
			ids = append(ids, id)
		}
		var storedIdentities []servermodels.OrganizationIdentity
		err := tx.NewSelect().Model(&storedIdentities).
			ColumnExpr("oi.id, oi.type").
			Where("oi.organization_id = ?", identity.Organization.ID).
			Apply(identityaction.ApplyActiveMemberConditions).
			Where("oi.id IN (?)", bun.In(ids)).
			Scan(ctx)
		if err != nil {
			return err
		}
		if len(storedIdentities) != len(ids) {
			return ErrMemberInvalid
		}
		for _, storedIdentity := range storedIdentities {
			if uniqueIDs[storedIdentity.ID] != domain.OrganizationIdentityType(storedIdentity.Type) {
				return ErrMemberInvalid
			}
		}
		relations := make([]servermodels.TeamMember, 0, len(ids))
		for _, id := range ids {
			relations = append(relations, servermodels.TeamMember{
				OrganizationID:  identity.Organization.ID,
				TeamID:          teamID,
				IdentityID:      id,
				CreatedByUserID: identity.User.ID,
			})
		}
		var added []string
		if err := tx.NewInsert().Model(&relations).
			Column("organization_id", "team_id", "identity_id", "created_by_user_id").
			On("CONFLICT (organization_id, team_id, identity_id) DO NOTHING").
			Returning("identity_id").
			Scan(ctx, &added); err != nil {
			return err
		}
		// 新加入的成员改变队列在线情况，通知企业全部网站访客重新读取接待状态，并为新加入的真人成员补分配。
		if len(added) > 0 {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
		}
		for _, id := range added {
			if uniqueIDs[id] != domain.OrganizationIdentityTypeUser {
				continue
			}
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{OrganizationID: identity.Organization.ID, IdentityID: id}); err != nil {
				return err
			}
		}
		team, err = loadTeam(ctx, tx, identity.Organization.ID, teamID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("add team members: %w", err)
	}
	return team, nil
}
