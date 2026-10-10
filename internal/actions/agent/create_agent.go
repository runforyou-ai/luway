//go:build server

// Package agent 实现企业 AI 员工操作。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"uuid"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// CreateAgentAction 创建企业 AI 员工。
type CreateAgentAction struct{ db *bun.DB }

// NewCreateAgentAction 创建 AI 员工新增操作。
func NewCreateAgentAction(db *bun.DB) *CreateAgentAction {
	return &CreateAgentAction{db: db}
}

// Execute 创建 AI 员工、当前执行配置和团队关系，转人工默认进入公共队列。
func (a *CreateAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, input CreateInput) (*Agent, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameRequired}}
	}
	if !domain.IdentityDisplayNameValid(input.DisplayName) {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameInvalid}}
	}
	serviceAudiences := normalizeServiceAudiences(input.ServiceAudiences)
	executionInput, err := normalizeExecutionInput(input.Execution)
	if err != nil {
		return nil, err
	}
	var output *Agent
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 新 AI 员工可能成为可接待身份，通知企业全部网站访客重新读取接待状态。
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		teamIDs, teams, err := validateAndLoadTeams(ctx, tx, identity.Workspace.ID, input.TeamIDs)
		if err != nil {
			return err
		}
		model, err := lockManagedExecutionModel(ctx, tx, identity.Workspace.ID, *executionInput.Managed)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Workspace.ID, executionInput); err != nil {
			return err
		}
		revisionID := uuid.NewV7()
		workspaceIdentity := &servermodels.WorkspaceIdentity{
			WorkspaceID: identity.Workspace.ID,
			Type:        string(domain.WorkspaceIdentityTypeAgent),
			DisplayName: input.DisplayName,
			WorkStatus:  string(domain.WorkStatusWorking),
		}
		// 传入头像时激活已上传的图片并随身份一起写入。
		if input.AvatarFileID != "" {
			avatarFileID, err := fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, nil)
			if err != nil {
				return err
			}
			workspaceIdentity.AvatarFileID = avatarFileID
		}
		if _, err := tx.NewInsert().Model(workspaceIdentity).
			Column("workspace_id", "type", "display_name", "avatar_file_id", "work_status").
			Returning("id, created_at").
			Exec(ctx); err != nil {
			return err
		}
		agent := &servermodels.Agent{
			IdentityID:       workspaceIdentity.ID,
			WorkspaceID:      identity.Workspace.ID,
			ActiveRevisionID: revisionID.String(),
			Status:           string(domain.IdentityStatusActive),
			ServiceAudiences: serviceAudiences,
		}
		if _, err := tx.NewInsert().Model(agent).
			Column("identity_id", "workspace_id", "active_revision_id", "status", "service_audiences").
			Returning("id").
			Exec(ctx); err != nil {
			return err
		}
		execution, err := insertExecutionRevision(ctx, tx, identity, agent.ID, revisionID.String(), executionInput, model, []domain.BusinessSystemGrant{})
		if err != nil {
			return err
		}
		if len(teamIDs) > 0 {
			relations := arr.Map(teamIDs, func(teamID string) servermodels.TeamMember {
				return servermodels.TeamMember{
					WorkspaceID:     identity.Workspace.ID,
					TeamID:          teamID,
					IdentityID:      workspaceIdentity.ID,
					CreatedByUserID: identity.User.ID,
				}
			})
			if _, err := tx.NewInsert().Model(&relations).
				Column("workspace_id", "team_id", "identity_id", "created_by_user_id").
				Exec(ctx); err != nil {
				return err
			}
		}
		output = &Agent{ID: agent.ID, IdentityID: workspaceIdentity.ID, DisplayName: workspaceIdentity.DisplayName, AvatarFileID: workspaceIdentity.AvatarFileID, ServiceAudiences: serviceAudiences, Status: domain.IdentityStatus(agent.Status), WorkStatus: domain.WorkStatus(workspaceIdentity.WorkStatus), Teams: teams, Execution: execution, CreatedAt: workspaceIdentity.CreatedAt}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return output, nil
}

// validateAndLoadTeams 规范化团队编号并锁定当前企业中的团队，团队不存在时返回字段错误。
func validateAndLoadTeams(ctx context.Context, db bun.IDB, workspaceID string, values []string) ([]string, []TeamSummary, error) {
	teamIDs, _ := common.NormalizeUUIDs(values)
	teams, err := teamaction.LockTeams(ctx, db, workspaceID, teamIDs)
	if errors.Is(err, teamaction.ErrNotFound) {
		return nil, nil, &common.FieldError{Fields: map[string]common.FieldCode{"teamIds": ValidationTeamInvalid}}
	}
	if err != nil {
		return nil, nil, err
	}
	return teamIDs, teams, nil
}
