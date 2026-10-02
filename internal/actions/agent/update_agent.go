//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// UpdateAgentAction 修改企业 AI 员工。
type UpdateAgentAction struct {
	db       *bun.DB
	returner ServiceSessionReturner
}

// NewUpdateAgentAction 创建 AI 员工修改操作。
func NewUpdateAgentAction(db *bun.DB, returner ServiceSessionReturner) *UpdateAgentAction {
	return &UpdateAgentAction{db: db, returner: returner}
}

// Execute 在事务中保存 AI 员工基本资料、服务对象、转人工团队、负责人、头像和工作状态；服务对象去掉客户时把其负责的开放服务周期退回原队列。
func (a *UpdateAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, input UpdateInput) (*Agent, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameRequired}}
	}
	if !domain.IdentityDisplayNameValid(input.DisplayName) {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameInvalid}}
	}
	if !common.ValidUUID(agentID) {
		return nil, ErrNotFound
	}
	if !input.WorkStatus.Valid() {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"workStatus": ValidationWorkStatusInvalid}}
	}
	serviceAudiences, err := normalizeServiceAudiences(input.ServiceAudiences)
	if err != nil {
		return nil, err
	}
	var handoffTeamID *string
	if input.HandoffTeamID = strings.TrimSpace(input.HandoffTeamID); input.HandoffTeamID != "" {
		if !common.ValidUUID(input.HandoffTeamID) {
			return nil, &common.FieldError{Fields: map[string]common.FieldCode{"handoffTeamId": ValidationHandoffTeamInvalid}}
		}
		handoffTeamID = &input.HandoffTeamID
	}
	var responsibleUserID *string
	if input.ResponsibleUserID = strings.TrimSpace(input.ResponsibleUserID); input.ResponsibleUserID != "" {
		if !common.ValidUUID(input.ResponsibleUserID) {
			return nil, &common.FieldError{Fields: map[string]common.FieldCode{"responsibleUserId": ValidationResponsibleInvalid}}
		}
		responsibleUserID = &input.ResponsibleUserID
	}
	var output *Agent
	var cancelledRunIDs []string
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		teamIDs, valid := common.NormalizeUUIDs(input.TeamIDs)
		if !valid {
			return &common.FieldError{Fields: map[string]common.FieldCode{"teamIds": ValidationTeamInvalid}}
		}
		// 所属团队与转人工团队在同一次查询中按编号顺序取 FOR KEY SHARE，与团队删除串行。
		lockedTeamIDs := teamIDs
		if handoffTeamID != nil && !slices.Contains(teamIDs, *handoffTeamID) {
			lockedTeamIDs = append(slices.Clone(teamIDs), *handoffTeamID)
		}
		if _, err := teamaction.LockTeams(ctx, tx, identity.Organization.ID, lockedTeamIDs); errors.Is(err, teamaction.ErrNotFound) {
			// 转人工团队不存在时报在转人工团队字段，否则报在所属团队字段。
			if handoffTeamID != nil {
				exists, err := tx.NewSelect().Model((*servermodels.Team)(nil)).
					Where("organization_id = ? AND id = ?", identity.Organization.ID, *handoffTeamID).
					Exists(ctx)
				if err != nil {
					return err
				}
				if !exists {
					return &common.FieldError{Fields: map[string]common.FieldCode{"handoffTeamId": ValidationHandoffTeamInvalid}}
				}
			}
			return &common.FieldError{Fields: map[string]common.FieldCode{"teamIds": ValidationTeamInvalid}}
		} else if err != nil {
			return err
		}
		storedAgent := &servermodels.Agent{}
		err = tx.NewSelect().Model(storedAgent).
			Column("a.identity_id", "a.status", "a.service_audiences", "a.responsible_user_id").
			Where("a.organization_id = ?", identity.Organization.ID).
			Where("a.id = ?", agentID).
			Where(serviceAgentCondition).
			For("UPDATE OF a").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if domain.IdentityStatus(storedAgent.Status) == domain.IdentityStatusInactive && input.WorkStatus != domain.WorkStatusOffDuty {
			return &common.FieldError{Fields: map[string]common.FieldCode{"workStatus": ValidationWorkStatusUnavailable}}
		}
		// 新指定的负责人须为本企业在职成员，保留原负责人时不校验其当前状态。
		if responsibleUserID != nil && (storedAgent.ResponsibleUserID == nil || *storedAgent.ResponsibleUserID != *responsibleUserID) {
			exists, err := tx.NewSelect().Model((*servermodels.User)(nil)).
				Where("organization_id = ? AND id = ? AND status = ?", identity.Organization.ID, *responsibleUserID, domain.IdentityStatusActive).
				Exists(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return &common.FieldError{Fields: map[string]common.FieldCode{"responsibleUserId": ValidationResponsibleInvalid}}
			}
		}
		// 先锁定身份，与以该身份为目标的入站路由和转交串行。
		locked, err := lockAgentIdentity(ctx, tx, identity.Organization.ID, storedAgent.IdentityID)
		if err != nil {
			return err
		}
		// 传入新头像时激活该图片，替换下来的旧头像交给清理任务。
		var nextAvatarFileID *string
		if input.AvatarFileID != "" {
			nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, locked.AvatarFileID)
			if err != nil {
				return err
			}
			if err := fileaction.RetireLinkedImage(ctx, tx, identity.Organization.ID, locked.AvatarFileID, nextAvatarFileID); err != nil {
				return err
			}
		}
		var displayChanged bool
		err = tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("avatar_file_id = COALESCE(?, avatar_file_id)", nextAvatarFileID).
			Set("work_status_updated_at = CASE WHEN work_status <> ? THEN now() ELSE work_status_updated_at END", input.WorkStatus).
			Set("work_status = ?", input.WorkStatus).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", storedAgent.IdentityID).
			Where("type = ?", domain.OrganizationIdentityTypeAgent).
			Returning("(old.display_name, old.avatar_file_id) IS DISTINCT FROM (new.display_name, new.avatar_file_id)").
			Scan(ctx, &displayChanged)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("service_audiences = ?", pgdialect.Array(serviceAudiences)).
			Set("handoff_team_id = ?", handoffTeamID).
			Set("responsible_user_id = ?", responsibleUserID).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", agentID).
			Exec(ctx); err != nil {
			return err
		}
		if err := teamaction.ReplaceIdentityTeams(ctx, tx, identity, storedAgent.IdentityID, teamIDs); err != nil {
			return err
		}
		// 名称、头像或是否服务客户实际变化时通知企业全部网站访客重新读取接待状态。
		servedCustomers, servesCustomers := slices.Contains(storedAgent.ServiceAudiences, domain.ServiceAudienceCustomer), slices.Contains(serviceAudiences, domain.ServiceAudienceCustomer)
		if displayChanged || servedCustomers != servesCustomers {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
		}
		// 负责人实际变化时通知成员重新读取本人负责的待补知识。
		if common.StringValue(storedAgent.ResponsibleUserID) != common.StringValue(responsibleUserID) {
			realtime.Notify(ctx, realtime.ServiceInboxKnowledgeGapsChanged(identity.Organization.ID))
		}
		// 服务对象去掉客户时退回渠道来源的开放周期，去掉员工时退回单聊的开放周期。
		removed := make([]domain.ServiceSource, 0, 2)
		if servedCustomers && !servesCustomers {
			removed = append(removed, domain.ServiceSourceChannel)
		}
		if slices.Contains(storedAgent.ServiceAudiences, domain.ServiceAudienceEmployee) && !slices.Contains(serviceAudiences, domain.ServiceAudienceEmployee) {
			removed = append(removed, domain.ServiceSourceDirect)
		}
		if len(removed) > 0 {
			cancelledRunIDs, err = a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Organization.ID, storedAgent.IdentityID, uuid.NewV7().String(), removed)
			if err != nil {
				return err
			}
		}
		// 名称或头像实际变化时，在资料写入与退回完成后推进展示该 AI 员工的会话版本；退回已锁定其负责的会话。
		if displayChanged {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, storedAgent.IdentityID); err != nil {
				return err
			}
		}
		output, err = loadAgent(ctx, tx, identity.Organization.ID, agentID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}
	a.returner.CancelRunContexts(cancelledRunIDs)
	return output, nil
}
