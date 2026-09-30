//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RouteSnapshot 定义渠道路由解析出的客服处理周期负责人与团队；两者都为空表示公共队列。
type RouteSnapshot struct {
	TeamID             *string
	TeamName           *string
	AssigneeIdentityID *string
	AssigneeName       *string
	AssigneeType       domain.OrganizationIdentityType
}

// SessionTarget 返回路由快照对应的转交去向与名称快照。
func (r RouteSnapshot) Target() domain.ServiceSessionTarget {
	switch {
	case r.AssigneeIdentityID != nil:
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: r.AssigneeIdentityID, DisplayName: r.AssigneeName}
	case r.TeamID != nil:
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetTeam, TeamID: r.TeamID, TeamName: r.TeamName}
	default:
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetPublicQueue}
	}
}

// ResolveNewSessionRoute 按渠道初始目标、失败团队、公共队列的顺序解析新客服处理周期的路由，不在工作中的真人成员视为不可用；目标身份与团队取 FOR KEY SHARE，调用方须在进入会话锁之前调用。
func ResolveNewSessionRoute(ctx context.Context, db bun.IDB, channel *servermodels.Channel) (RouteSnapshot, error) {
	return resolveNewSessionRoute(ctx, db, channel, true)
}

// PeekNewSessionRoute 按 ResolveNewSessionRoute 的顺序解析新客服处理周期当前会进入的路由，不加锁。
func PeekNewSessionRoute(ctx context.Context, db bun.IDB, channel *servermodels.Channel) (RouteSnapshot, error) {
	return resolveNewSessionRoute(ctx, db, channel, false)
}

// resolveNewSessionRoute 依次尝试渠道初始目标与失败团队，均不可用时进入公共队列；lock 为 true 时目标身份与团队取 FOR KEY SHARE 并记录路由降级。
func resolveNewSessionRoute(ctx context.Context, db bun.IDB, channel *servermodels.Channel, lock bool) (RouteSnapshot, error) {
	channelType := domain.ChannelType(channel.Type)
	if route, available, err := availableRoute(ctx, db, channel.OrganizationID, channelType, domain.ChannelRoutingTargetType(channel.InitialRoutingTargetType), channel.InitialRoutingTargetID, lock); err != nil {
		return RouteSnapshot{}, fmt.Errorf("resolve message channel initial route: %w", err)
	} else if available {
		return route, nil
	}
	if lock {
		slog.Warn("消息渠道初始路由不可用", "organization_id", channel.OrganizationID, "channel_id", channel.ID, "target_type", channel.InitialRoutingTargetType)
	}
	// 失败去向只取团队，其余取值进入公共队列。
	if teamID := ChannelHandoffTeamID(channel); teamID != nil {
		if route, available, err := availableTeamRoute(ctx, db, channel.OrganizationID, *teamID, lock); err != nil {
			return RouteSnapshot{}, fmt.Errorf("resolve message channel fallback route: %w", err)
		} else if available {
			return route, nil
		}
		if lock {
			slog.Warn("消息渠道失败团队不可用，进入公共队列", "organization_id", channel.OrganizationID, "channel_id", channel.ID, "team_id", *teamID)
		}
	}
	return RouteSnapshot{}, nil
}

// ResolveHandoffQueue 解析 AI 转人工进入的队列：依次取咨询分类对应的团队、入口配置的失败团队，均未指定或不可用时进入公共队列；lock 为 true 时团队取 FOR KEY SHARE。
func ResolveHandoffQueue(ctx context.Context, db bun.IDB, organizationID string, categoryTeamID, fallbackTeamID *string, lock bool) (RouteSnapshot, error) {
	for _, teamID := range []*string{categoryTeamID, fallbackTeamID} {
		if teamID == nil {
			continue
		}
		route, available, err := availableTeamRoute(ctx, db, organizationID, *teamID, lock)
		if err != nil {
			return RouteSnapshot{}, fmt.Errorf("resolve handoff team: %w", err)
		}
		if available {
			return route, nil
		}
		slog.Warn("转人工团队不可用，按下一去向转交", "organization_id", organizationID, "team_id", *teamID)
	}
	return RouteSnapshot{}, nil
}

// ChannelHandoffTeamID 返回渠道失败去向中的团队编号，失败去向不是团队时返回空。
func ChannelHandoffTeamID(channel *servermodels.Channel) *string {
	if domain.ChannelRoutingTargetType(channel.FallbackRoutingTargetType) != domain.ChannelRoutingTargetTypeTeam {
		return nil
	}
	return channel.FallbackRoutingTargetID
}

// LoadConversationChannel 读取客户会话所属的消息渠道。
func LoadConversationChannel(ctx context.Context, db bun.IDB, organizationID, conversationID string) (*servermodels.Channel, error) {
	channel := &servermodels.Channel{}
	if err := db.NewSelect().Model(channel).
		Join("JOIN contact_channel_identities AS cci ON cci.channel_id = c.id AND cci.organization_id = c.organization_id").
		Join("JOIN channel_conversations AS cc ON cc.contact_channel_identity_id = cci.id AND cc.organization_id = cci.organization_id").
		Where("cc.organization_id = ? AND cc.conversation_id = ?", organizationID, conversationID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load customer conversation channel: %w", err)
	}
	return channel, nil
}

// ServiceSessionQueueTarget 返回服务周期当前所属队列的去向快照，团队已删除时按公共队列处理。
func ServiceSessionQueueTarget(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (domain.ServiceSessionTarget, error) {
	if session.TeamID == nil {
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetPublicQueue}, nil
	}
	var name string
	err := db.NewSelect().Model((*servermodels.Team)(nil)).Column("t.name").
		Where("t.organization_id = ? AND t.id = ?", session.OrganizationID, *session.TeamID).
		Scan(ctx, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetPublicQueue}, nil
	}
	if err != nil {
		return domain.ServiceSessionTarget{}, fmt.Errorf("load service session queue team: %w", err)
	}
	return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetTeam, TeamID: session.TeamID, TeamName: &name}, nil
}

// availableRoute 判断路由目标当前是否可用并返回对应快照；lock 为 true 时记录目标不可用的原因。
func availableRoute(ctx context.Context, db bun.IDB, organizationID string, channelType domain.ChannelType, targetType domain.ChannelRoutingTargetType, targetID *string, lock bool) (RouteSnapshot, bool, error) {
	switch targetType {
	case domain.ChannelRoutingTargetTypePublicQueue:
		return RouteSnapshot{}, true, nil
	case domain.ChannelRoutingTargetTypeTeam:
		if targetID == nil {
			return RouteSnapshot{}, false, nil
		}
		return availableTeamRoute(ctx, db, organizationID, *targetID, lock)
	case domain.ChannelRoutingTargetTypeMember:
		if targetID == nil {
			return RouteSnapshot{}, false, nil
		}
		load := identityaction.LoadActiveServiceHandlingIdentity
		if lock {
			load = identityaction.LockActiveServiceHandlingIdentity
		}
		identity, err := load(ctx, db, organizationID, *targetID)
		if errors.Is(err, sql.ErrNoRows) {
			return RouteSnapshot{}, false, nil
		}
		if err != nil {
			return RouteSnapshot{}, false, err
		}
		identityType := domain.OrganizationIdentityType(identity.Type)
		// 真人成员只在工作中时承接路由，AI 员工按接待资格承接。
		if identityType == domain.OrganizationIdentityTypeUser && domain.WorkStatus(identity.WorkStatus) != domain.WorkStatusWorking {
			if lock {
				slog.Info("消息渠道路由的成员不在工作中", "organization_id", organizationID, "identity_id", identity.ID, "work_status", identity.WorkStatus)
			}
			return RouteSnapshot{}, false, nil
		}
		if identityType == domain.OrganizationIdentityTypeAgent && !domain.ChannelSupportsAgentAssignee(channelType) {
			if lock {
				slog.Warn("消息渠道不支持 AI 员工作为负责人",
					"organization_id", organizationID,
					"channel_type", channelType,
					"agent_identity_id", identity.ID,
				)
			}
			return RouteSnapshot{}, false, nil
		}
		return RouteSnapshot{AssigneeIdentityID: &identity.ID, AssigneeName: &identity.DisplayName, AssigneeType: identityType}, true, nil
	default:
		return RouteSnapshot{}, false, nil
	}
}

// availableTeamRoute 判断团队当前能否承接队列并返回对应快照：lock 为 true 时团队取 FOR KEY SHARE，与团队删除互斥；已删除或没有开启接待真人成员的团队视为不可用。
func availableTeamRoute(ctx context.Context, db bun.IDB, organizationID, teamID string, lock bool) (RouteSnapshot, bool, error) {
	team := &servermodels.Team{}
	query := db.NewSelect().Model(team).Column("t.id", "t.name").
		Where("t.organization_id = ?", organizationID).
		Where("t.id = ?", teamID)
	if lock {
		query = query.For("KEY SHARE")
	}
	err := query.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteSnapshot{}, false, nil
	}
	if err != nil {
		return RouteSnapshot{}, false, err
	}
	available, err := identityaction.TeamHasServiceHandler(ctx, db, organizationID, team.ID)
	if err != nil {
		return RouteSnapshot{}, false, err
	}
	if !available {
		if lock {
			slog.Warn("路由团队没有可接待的真人成员", "organization_id", organizationID, "team_id", team.ID)
		}
		return RouteSnapshot{}, false, nil
	}
	return RouteSnapshot{TeamID: &team.ID, TeamName: &team.Name}, true, nil
}

// ResetChannelRoutingTarget 把引用指定对象的渠道路由重置为公共队列。
func ResetChannelRoutingTarget(ctx context.Context, db bun.IDB, organizationID string, targetType domain.ChannelRoutingTargetType, targetID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.Channel)(nil)).
		Set("initial_routing_target_type = ?", domain.ChannelRoutingTargetTypePublicQueue).
		Set("initial_routing_target_id = NULL").
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
		Where("initial_routing_target_type = ?", targetType).
		Where("initial_routing_target_id = ?", targetID).
		Exec(ctx); err != nil {
		return err
	}
	_, err := db.NewUpdate().Model((*servermodels.Channel)(nil)).
		Set("fallback_routing_target_type = ?", domain.ChannelRoutingTargetTypePublicQueue).
		Set("fallback_routing_target_id = NULL").
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
		Where("fallback_routing_target_type = ?", targetType).
		Where("fallback_routing_target_id = ?", targetID).
		Exec(ctx)
	return err
}
