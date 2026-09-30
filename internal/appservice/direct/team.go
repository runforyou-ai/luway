//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// ListTeams 返回企业团队列表。
func (o *directOperations) ListTeams(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamListInput) (appservice.TeamList, error) {
	output, err := o.listTeams.Execute(ctx, identity, teamaction.ListInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamList{}, o.teamError(ctx, meta, err, i18n.ErrorTeamListFailed, identity.Organization.ID, "")
	}
	teams := make([]appservice.Team, 0, len(output.Teams))
	for _, team := range output.Teams {
		teams = append(teams, teamFromAction(team))
	}
	return appservice.TeamList{Teams: teams, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetTeam 返回团队详情。
func (o *directOperations) GetTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string) (appservice.Team, error) {
	team, err := o.getTeam.Execute(ctx, identity, teamID)
	if err != nil {
		return appservice.Team{}, o.teamError(ctx, meta, err, i18n.ErrorTeamLoadFailed, identity.Organization.ID, teamID)
	}
	return teamFromAction(team), nil
}

// CreateTeam 创建企业团队。
func (o *directOperations) CreateTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamInput) (appservice.Team, error) {
	team, err := o.createTeam.Execute(ctx, identity, teamaction.Input{Name: input.Name, Description: input.Description})
	if err != nil {
		return appservice.Team{}, o.teamError(ctx, meta, err, i18n.ErrorTeamCreateFailed, identity.Organization.ID, "")
	}
	slog.Info("团队创建成功", "organization_id", identity.Organization.ID, "team_id", team.ID)
	return teamFromAction(*team), nil
}

// UpdateTeam 修改企业团队。
func (o *directOperations) UpdateTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamInput) (appservice.Team, error) {
	team, err := o.updateTeam.Execute(ctx, identity, teamID, teamaction.Input{Name: input.Name, Description: input.Description})
	if err != nil {
		return appservice.Team{}, o.teamError(ctx, meta, err, i18n.ErrorTeamUpdateFailed, identity.Organization.ID, teamID)
	}
	slog.Info("团队更新成功", "organization_id", identity.Organization.ID, "team_id", teamID)
	return teamFromAction(*team), nil
}

// DeleteTeam 删除企业团队及其成员关系。
func (o *directOperations) DeleteTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string) error {
	if err := o.deleteTeam.Execute(ctx, identity, teamID); err != nil {
		return o.teamError(ctx, meta, err, i18n.ErrorTeamDeleteFailed, identity.Organization.ID, teamID)
	}
	slog.Info("团队删除成功", "organization_id", identity.Organization.ID, "team_id", teamID)
	return nil
}

// ListTeamMembers 返回团队成员列表。
func (o *directOperations) ListTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberListInput) (appservice.TeamMemberList, error) {
	output, err := o.listTeamMembers.Execute(ctx, identity, teamID, teamaction.MemberListInput{
		Query: input.Query, WorkStatus: optionalDomain[appservice.WorkStatus, domain.WorkStatus](input.WorkStatus), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.TeamMemberList{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberListFailed, identity.Organization.ID, teamID)
	}
	return o.teamMemberList(ctx, meta, identity, teamID, output)
}

// teamMemberList 把团队成员查询结果转换为应用服务结构并补齐头像地址。
func (o *directOperations) teamMemberList(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, output teamaction.MemberListOutput) (appservice.TeamMemberList, error) {
	avatarFileIDs := make([]*string, 0, len(output.Members))
	for _, member := range output.Members {
		avatarFileIDs = append(avatarFileIDs, member.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.TeamMemberList{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberListFailed, identity.Organization.ID, teamID)
	}
	members := make([]appservice.TeamMember, 0, len(output.Members))
	for _, member := range output.Members {
		// 真人成员只有 users.id，AI 员工只有 agents.id，另一端为空。
		userID, agentID := "", ""
		if member.UserID != nil {
			userID = *member.UserID
		}
		if member.AgentID != nil {
			agentID = *member.AgentID
		}
		members = append(members, appservice.TeamMember{
			IdentityID: member.IdentityID, IdentityType: appservice.OrganizationIdentityType(member.IdentityType),
			UserID: userID, AgentID: agentID,
			DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID), WorkStatus: appservice.WorkStatus(member.WorkStatus), JoinedAt: member.JoinedAt,
		})
	}
	return appservice.TeamMemberList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// ListTeamMemberCandidates 返回尚未加入团队的企业身份。
func (o *directOperations) ListTeamMemberCandidates(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberCandidateInput) (appservice.TeamMemberCandidateList, error) {
	output, err := o.listTeamMemberCandidates.Execute(ctx, identity, teamID, teamaction.MemberCandidateInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamMemberCandidateList{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberListFailed, identity.Organization.ID, teamID)
	}
	avatarFileIDs := make([]string, 0, len(output.Members))
	for _, member := range output.Members {
		if member.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *member.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.TeamMemberCandidateList{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberListFailed, identity.Organization.ID, teamID)
	}
	members := make([]appservice.TeamMemberCandidate, 0, len(output.Members))
	for _, member := range output.Members {
		members = append(members, appservice.TeamMemberCandidate{
			IdentityType: appservice.OrganizationIdentityType(member.IdentityType), IdentityID: member.IdentityID,
			DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID),
		})
	}
	return appservice.TeamMemberCandidateList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// AddTeamMembers 将企业身份批量加入团队。
func (o *directOperations) AddTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberInput) (appservice.Team, error) {
	members := make([]teamaction.MemberIdentity, 0, len(input.Members))
	for _, member := range input.Members {
		members = append(members, teamaction.MemberIdentity{IdentityType: domain.OrganizationIdentityType(member.IdentityType), IdentityID: member.IdentityID})
	}
	team, err := o.addTeamMembers.Execute(ctx, identity, teamID, members)
	if err != nil {
		return appservice.Team{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberAddFailed, identity.Organization.ID, teamID)
	}
	slog.Info("团队成员添加成功", "organization_id", identity.Organization.ID, "team_id", teamID, "requested_member_count", len(members))
	return teamFromAction(*team), nil
}

// RemoveTeamMembers 将企业身份批量移出团队。
func (o *directOperations) RemoveTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberInput) (appservice.Team, error) {
	members := make([]teamaction.MemberIdentity, 0, len(input.Members))
	for _, member := range input.Members {
		members = append(members, teamaction.MemberIdentity{IdentityType: domain.OrganizationIdentityType(member.IdentityType), IdentityID: member.IdentityID})
	}
	team, err := o.removeTeamMembers.Execute(ctx, identity, teamID, members)
	if err != nil {
		return appservice.Team{}, o.teamError(ctx, meta, err, i18n.ErrorTeamMemberRemoveFailed, identity.Organization.ID, teamID)
	}
	slog.Info("团队成员移出成功", "organization_id", identity.Organization.ID, "team_id", teamID, "requested_member_count", len(members))
	return teamFromAction(*team), nil
}

// teamError 转换团队领域错误。
func (o *directOperations) teamError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, teamID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把团队校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			teamaction.ValidationNameRequired:       i18n.FieldTeamNameRequired,
			teamaction.ValidationNameTooLong:        i18n.FieldTeamNameTooLong,
			teamaction.ValidationNameDuplicate:      i18n.FieldTeamNameDuplicate,
			teamaction.ValidationDescriptionTooLong: i18n.FieldTeamDescriptionTooLong,
			teamaction.ValidationQueryInvalid:       i18n.FieldTeamQueryInvalid,
			teamaction.ValidationWorkStatusInvalid:  i18n.FieldWorkStatusInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, teamaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorTeamNotFound)
	}
	if errors.Is(err, teamaction.ErrMemberNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorTeamMemberNotFound)
	}
	if errors.Is(err, teamaction.ErrMemberInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	attributes := []any{"organization_id", organizationID, "failure", failureKey, "error", err}
	if teamID != "" {
		attributes = append(attributes, "team_id", teamID)
	}
	slog.Warn("团队操作失败", attributes...)
	return appservice.FailedError(meta, failureKey)
}

// teamFromAction 转换团队契约。
func teamFromAction(team teamaction.TeamRecord) appservice.Team {
	return appservice.Team{ID: team.ID, Name: team.Name, Description: team.Description, MemberCount: team.MemberCount, CreatedAt: team.CreatedAt, UpdatedAt: team.UpdatedAt}
}
