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

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// UpdateAgentAction 修改企业 AI 员工。
type UpdateAgentAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
	returner ServiceSessionReturner
}

// NewUpdateAgentAction 创建 AI 员工修改操作。
func NewUpdateAgentAction(db *bun.DB, enqueuer servertask.TxEnqueuer, returner ServiceSessionReturner) *UpdateAgentAction {
	return &UpdateAgentAction{db: db, enqueuer: enqueuer, returner: returner}
}

// Execute 在事务中保存 AI 员工基本资料、服务对象、转人工团队、负责人、工作区电脑及其授权、头像和工作状态；服务对象中被去掉的对象的开放服务周期退回原队列，
// 负责人变化时取消等待原负责人审批的操作。业务系统或工作区电脑的授权包含需要审批的操作时须指定负责人。
func (a *UpdateAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, input UpdateInput) (*Agent, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameRequired}}
	}
	if !domain.IdentityDisplayNameValid(input.DisplayName) {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameInvalid}}
	}
	serviceAudiences := normalizeServiceAudiences(input.ServiceAudiences)
	var handoffTeamID *string
	if input.HandoffTeamID != "" {
		handoffTeamID = &input.HandoffTeamID
	}
	var responsibleUserID *string
	if input.ResponsibleUserID != "" {
		responsibleUserID = &input.ResponsibleUserID
	}
	var computerID *string
	var computerGrant *domain.ToolGrant
	localAgents := []string{}
	if input.ComputerID != "" {
		if !slices.Contains(domain.ComputerOperationLevels, input.ComputerGrant.MaxLevel) {
			return nil, &common.FieldError{Fields: map[string]common.FieldCode{"computerGrant": ValidationComputerGrantInvalid}}
		}
		computerID, computerGrant, localAgents = &input.ComputerID, &input.ComputerGrant, NormalizeLocalAgents(input.LocalAgents)
	}
	if responsibleUserID == nil && computerGrant != nil && computerGrant.RequiresApproval() {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"responsibleUserId": ValidationResponsibleRequired}}
	}
	var output *Agent
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 锁定未撤销的工作区电脑，与电脑撤销串行。
		if computerID != nil {
			exists, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
				Where("cmp.id = ? AND cmp.workspace_id = ? AND cmp.kind = ? AND cmp.revoked_at IS NULL", *computerID, identity.Workspace.ID, domain.ComputerKindWorkspace).
				For("SHARE").
				Exists(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return &common.FieldError{Fields: map[string]common.FieldCode{"computerId": ValidationComputerInvalid}}
			}
		}
		teamIDs, _ := common.NormalizeUUIDs(input.TeamIDs)
		// 所属团队与转人工团队在同一次查询中按编号顺序取 FOR KEY SHARE，与团队删除串行。
		lockedTeamIDs := teamIDs
		if handoffTeamID != nil && !slices.Contains(teamIDs, *handoffTeamID) {
			lockedTeamIDs = append(slices.Clone(teamIDs), *handoffTeamID)
		}
		if _, err := teamaction.LockTeams(ctx, tx, identity.Workspace.ID, lockedTeamIDs); errors.Is(err, teamaction.ErrNotFound) {
			// 转人工团队不存在时报在转人工团队字段，否则报在所属团队字段。
			if handoffTeamID != nil {
				exists, err := tx.NewSelect().Model((*servermodels.Team)(nil)).
					Where("workspace_id = ? AND id = ?", identity.Workspace.ID, *handoffTeamID).
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
		err := tx.NewSelect().Model(storedAgent).
			Column("a.identity_id", "a.status", "a.service_audiences", "a.responsible_user_id").
			Where("a.workspace_id = ?", identity.Workspace.ID).
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
				Where("workspace_id = ? AND id = ? AND status = ?", identity.Workspace.ID, *responsibleUserID, domain.IdentityStatusActive).
				Exists(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return &common.FieldError{Fields: map[string]common.FieldCode{"responsibleUserId": ValidationResponsibleInvalid}}
			}
		}
		// 当前业务系统授权包含需要审批的操作时不能取消负责人。
		if responsibleUserID == nil {
			var configuration struct {
				Grants []domain.BusinessSystemGrant `bun:"business_systems,type:jsonb"`
			}
			if err := tx.NewSelect().TableExpr("agent_revisions AS ar").
				ColumnExpr("ar.configuration->'businessSystems' AS business_systems").
				Join("JOIN agents AS a ON a.active_revision_id = ar.id AND a.workspace_id = ar.workspace_id").
				Where("a.workspace_id = ? AND a.id = ?", identity.Workspace.ID, agentID).
				Scan(ctx, &configuration); err != nil {
				return err
			}
			if slices.ContainsFunc(configuration.Grants, domain.BusinessSystemGrant.RequiresApproval) {
				return &common.FieldError{Fields: map[string]common.FieldCode{"responsibleUserId": ValidationResponsibleRequired}}
			}
		}
		// 先锁定身份，与以该身份为目标的入站路由和转交串行。
		locked, err := lockAgentIdentity(ctx, tx, identity.Workspace.ID, storedAgent.IdentityID)
		if err != nil {
			return err
		}
		// 传入新头像时激活该图片，替换下来的旧头像交给清理任务。
		var nextAvatarFileID *string
		if input.AvatarFileID != "" {
			nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, locked.AvatarFileID)
			if err != nil {
				return err
			}
			if err := fileaction.RetireLinkedImage(ctx, tx, identity.Workspace.ID, locked.AvatarFileID, nextAvatarFileID); err != nil {
				return err
			}
		}
		var displayChanged bool
		err = tx.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("avatar_file_id = COALESCE(?, avatar_file_id)", nextAvatarFileID).
			Set("work_status_updated_at = CASE WHEN work_status <> ? THEN now() ELSE work_status_updated_at END", input.WorkStatus).
			Set("work_status = ?", input.WorkStatus).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", storedAgent.IdentityID).
			Where("type = ?", domain.WorkspaceIdentityTypeAgent).
			Returning("(old.display_name, old.avatar_file_id) IS DISTINCT FROM (new.display_name, new.avatar_file_id)").
			Scan(ctx, &displayChanged)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("service_audiences = ?", pgdialect.Array(serviceAudiences)).
			Set("handoff_team_id = ?", handoffTeamID).
			Set("responsible_user_id = ?", responsibleUserID).
			Set("computer_id = ?", computerID).
			Set("computer_grant = ?", computerGrant).
			Set("local_agents = ?", localAgentsJSON(localAgents)).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", agentID).
			Exec(ctx); err != nil {
			return err
		}
		if err := teamaction.ReplaceIdentityTeams(ctx, tx, identity, storedAgent.IdentityID, teamIDs); err != nil {
			return err
		}
		if err := releaseStaleLocalAgentSessions(ctx, tx, identity.Workspace.ID, agentID, computerID, localAgents); err != nil {
			return err
		}
		// 名称、头像或是否服务客户实际变化时通知企业全部网站访客重新读取接待状态。
		servedCustomers, servesCustomers := slices.Contains(storedAgent.ServiceAudiences, domain.ServiceAudienceCustomer), slices.Contains(serviceAudiences, domain.ServiceAudienceCustomer)
		if displayChanged || servedCustomers != servesCustomers {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		}
		// 负责人实际变化时通知成员重新读取本人负责的待补知识与待核对操作，并取消等待原负责人审批的操作。
		if support.Deref(storedAgent.ResponsibleUserID) != support.Deref(responsibleUserID) {
			realtime.Notify(ctx, realtime.ServiceInboxKnowledgeGapsChanged(identity.Workspace.ID))
			for _, userID := range []*string{storedAgent.ResponsibleUserID, responsibleUserID} {
				if userID != nil {
					realtime.Notify(ctx, realtime.UserToolDecisionsChanged(identity.Workspace.ID, *userID))
				}
			}
			if err := agentprocess.CancelReassignedApprovals(ctx, tx, a.enqueuer, identity.Workspace.ID, storedAgent.IdentityID); err != nil {
				return err
			}
		}
		// 退回服务对象中被去掉的对象的开放周期。
		removed := arr.Diff(storedAgent.ServiceAudiences, serviceAudiences)
		if len(removed) > 0 {
			if err := a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Workspace.ID, storedAgent.IdentityID, uuid.NewV7().String(), removed); err != nil {
				return err
			}
		}
		// 名称或头像实际变化时，在资料写入与退回完成后推进展示该 AI 员工的会话版本；退回已锁定其负责的会话。
		if displayChanged {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, storedAgent.IdentityID); err != nil {
				return err
			}
		}
		output, err = loadAgent(ctx, tx, identity.Workspace.ID, agentID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update agent: %w", err)
	}
	return output, nil
}
