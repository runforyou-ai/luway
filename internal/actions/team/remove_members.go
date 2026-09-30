//go:build server

package team

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RemoveMembersAction 批量移出团队成员。
type RemoveMembersAction struct{ db *bun.DB }

// NewRemoveMembersAction 创建团队成员批量移出操作。
func NewRemoveMembersAction(db *bun.DB) *RemoveMembersAction {
	return &RemoveMembersAction{db: db}
}

// Execute 规范化并校验成员后批量解除团队关系。
func (a *RemoveMembersAction) Execute(ctx context.Context, identity *servermodels.Identity, teamID string, members []MemberIdentity) (*TeamRecord, error) {
	var team *TeamRecord
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockTeam(ctx, tx, identity.Organization.ID, teamID); err != nil {
			return err
		}
		identityIDs := make([]string, 0, len(members))
		pairs := make([][]any, 0, len(members))
		seen := make(map[string]struct{}, len(members))
		for _, member := range members {
			identityID, identityIDValid := common.NormalizeUUID(member.IdentityID)
			if (member.IdentityType != domain.OrganizationIdentityTypeUser && member.IdentityType != domain.OrganizationIdentityTypeAgent) || !identityIDValid {
				return ErrMemberInvalid
			}
			if _, ok := seen[identityID]; ok {
				continue
			}
			seen[identityID] = struct{}{}
			identityIDs = append(identityIDs, identityID)
			pairs = append(pairs, []any{identityID, member.IdentityType})
		}
		if len(identityIDs) == 0 {
			return ErrMemberInvalid
		}
		matched, err := tx.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
			Where("organization_id = ?", identity.Organization.ID).
			Where("(id, type) IN (?)", bun.In(pairs)).
			Count(ctx)
		if err != nil {
			return err
		}
		if matched != len(identityIDs) {
			return ErrMemberInvalid
		}
		removed := make([]string, 0, len(identityIDs))
		if err := tx.NewDelete().Model((*servermodels.TeamMember)(nil)).
			Where("organization_id = ?", identity.Organization.ID).
			Where("team_id = ?", teamID).
			Where("identity_id IN (?)", bun.In(identityIDs)).
			Returning("identity_id::text").
			Scan(ctx, &removed); err != nil {
			return err
		}
		if len(removed) != len(identityIDs) {
			return ErrMemberNotFound
		}
		// 团队成员变化会改变队列在线情况，通知企业全部网站访客重新读取接待状态。
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
		team, err = loadTeam(ctx, tx, identity.Organization.ID, teamID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("remove team members: %w", err)
	}
	return team, nil
}
