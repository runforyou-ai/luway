//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// validateRoutingTarget 校验并锁定会话流转目标，目标须属于当前企业且可用。
func validateRoutingTarget(ctx context.Context, db bun.IDB, organizationID string, channelType domain.ChannelType, field string, target RoutingTarget) error {
	if target.Type == domain.ChannelRoutingTargetTypePublicQueue {
		return nil
	}
	var available bool
	var err error
	switch target.Type {
	case domain.ChannelRoutingTargetTypeTeam:
		// 对团队取 FOR KEY SHARE，与团队删除互斥。
		var teamID string
		err = db.NewSelect().Model((*servermodels.Team)(nil)).Column("t.id").
			Where("t.organization_id = ? AND t.id = ?", organizationID, target.ID).
			For("KEY SHARE").
			Scan(ctx, &teamID)
		available = err == nil
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	case domain.ChannelRoutingTargetTypeMember:
		identity, loadErr := identityaction.LockActiveServiceHandlingIdentity(ctx, db, organizationID, target.ID)
		if errors.Is(loadErr, sql.ErrNoRows) {
			break
		}
		if loadErr != nil {
			return loadErr
		}
		available = domain.OrganizationIdentityType(identity.Type) != domain.OrganizationIdentityTypeAgent || domain.ChannelSupportsAgentAssignee(channelType)
	}
	if err != nil {
		return err
	}
	if !available {
		return &ValidationError{Fields: map[string]ValidationCode{field: ValidationRoutingTargetInvalid}}
	}
	return nil
}

// routingTargetID 返回可写入渠道记录的目标编号。
func routingTargetID(target RoutingTarget) *string {
	if target.Type == domain.ChannelRoutingTargetTypePublicQueue {
		return nil
	}
	return &target.ID
}
