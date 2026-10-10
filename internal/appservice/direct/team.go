//go:build server

package direct

import (
	"context"
	"log/slog"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// ListTeams 返回企业团队列表。
func (o *directoryOps) ListTeams(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamListInput) (appservice.TeamList, error) {
	output, err := o.listTeams.Execute(ctx, identity, teamaction.ListInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamList{}, teamError(meta, err, i18n.ErrorTeamListFailed)
	}
	teams := arr.Map(output.Teams, teamFromAction)
	return appservice.TeamList{Teams: teams, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetTeam 返回团队详情。
func (o *directoryOps) GetTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string) (appservice.Team, error) {
	team, err := o.getTeam.Execute(ctx, identity, teamID)
	if err != nil {
		return appservice.Team{}, teamError(meta, err, i18n.ErrorTeamLoadFailed)
	}
	return teamFromAction(team), nil
}

// CreateTeam 创建企业团队。
func (o *directoryOps) CreateTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamInput) (appservice.Team, error) {
	team, err := o.createTeam.Execute(ctx, identity, teamaction.Input{Name: input.Name, Description: input.Description})
	if err != nil {
		return appservice.Team{}, teamError(meta, err, i18n.ErrorTeamCreateFailed)
	}
	slog.InfoContext(ctx, "团队创建成功", "team_id", team.ID)
	return teamFromAction(*team), nil
}

// UpdateTeam 修改企业团队。
func (o *directoryOps) UpdateTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamInput) (appservice.Team, error) {
	team, err := o.updateTeam.Execute(ctx, identity, teamID, teamaction.Input{Name: input.Name, Description: input.Description})
	if err != nil {
		return appservice.Team{}, teamError(meta, err, i18n.ErrorTeamUpdateFailed)
	}
	slog.InfoContext(ctx, "团队更新成功", "team_id", teamID)
	return teamFromAction(*team), nil
}

// DeleteTeam 删除企业团队及其成员关系。
func (o *directoryOps) DeleteTeam(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string) error {
	if err := o.deleteTeam.Execute(ctx, identity, teamID); err != nil {
		return teamError(meta, err, i18n.ErrorTeamDeleteFailed)
	}
	slog.InfoContext(ctx, "团队删除成功", "team_id", teamID)
	return nil
}

// ListTeamMembers 返回团队成员列表。
func (o *directoryOps) ListTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberListInput) (appservice.TeamMemberList, error) {
	output, err := o.listTeamMembers.Execute(ctx, identity, teamID, teamaction.MemberListInput{
		Query: input.Query, WorkStatus: domain.WorkStatus(support.Deref(input.WorkStatus)), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.TeamMemberList{}, teamError(meta, err, i18n.ErrorTeamMemberListFailed)
	}
	return o.teamMemberList(ctx, meta, identity, output)
}

// teamMemberList 把团队成员查询结果转换为应用服务结构并补齐头像地址。
func (o *directoryOps) teamMemberList(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, output teamaction.MemberListOutput) (appservice.TeamMemberList, error) {
	avatarFileIDs := arr.Map(output.Members, func(member teamaction.Member) *string { return member.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.TeamMemberList{}, teamError(meta, err, i18n.ErrorTeamMemberListFailed)
	}
	members := arr.Map(output.Members, func(member teamaction.Member) appservice.TeamMember {
		// 真人成员只有 users.id，AI 员工只有 agents.id，另一端为空。
		return appservice.TeamMember{
			IdentityID: member.IdentityID, IdentityType: member.IdentityType,
			UserID: support.Deref(member.UserID), AgentID: support.Deref(member.AgentID),
			DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID), WorkStatus: member.WorkStatus, JoinedAt: member.JoinedAt,
		}
	})
	return appservice.TeamMemberList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// ListTeamMemberCandidates 返回尚未加入团队的企业身份。
func (o *directoryOps) ListTeamMemberCandidates(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberCandidateInput) (appservice.TeamMemberCandidateList, error) {
	output, err := o.listTeamMemberCandidates.Execute(ctx, identity, teamID, teamaction.MemberCandidateInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamMemberCandidateList{}, teamError(meta, err, i18n.ErrorTeamMemberListFailed)
	}
	avatarFileIDs := arr.FilterMap(output.Members, func(member teamaction.MemberCandidate) (string, bool) {
		return support.Deref(member.AvatarFileID), member.AvatarFileID != nil
	})
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.TeamMemberCandidateList{}, teamError(meta, err, i18n.ErrorTeamMemberListFailed)
	}
	members := arr.Map(output.Members, func(member teamaction.MemberCandidate) appservice.TeamMemberCandidate {
		return appservice.TeamMemberCandidate{
			IdentityType: member.IdentityType, IdentityID: member.IdentityID,
			DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID),
		}
	})
	return appservice.TeamMemberCandidateList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// AddTeamMembers 将企业身份批量加入团队。
func (o *directoryOps) AddTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberInput) (appservice.Team, error) {
	members := arr.Map(input.Members, func(member appservice.TeamMemberIdentityInput) teamaction.MemberIdentity {
		return teamaction.MemberIdentity{IdentityType: member.IdentityType, IdentityID: member.IdentityID}
	})
	team, err := o.addTeamMembers.Execute(ctx, identity, teamID, members)
	if err != nil {
		return appservice.Team{}, teamError(meta, err, i18n.ErrorTeamMemberAddFailed)
	}
	slog.InfoContext(ctx, "团队成员添加成功", "team_id", teamID, "requested_member_count", len(members))
	return teamFromAction(*team), nil
}

// RemoveTeamMembers 将企业身份批量移出团队。
func (o *directoryOps) RemoveTeamMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, teamID string, input appservice.TeamMemberInput) (appservice.Team, error) {
	members := arr.Map(input.Members, func(member appservice.TeamMemberIdentityInput) teamaction.MemberIdentity {
		return teamaction.MemberIdentity{IdentityType: member.IdentityType, IdentityID: member.IdentityID}
	})
	team, err := o.removeTeamMembers.Execute(ctx, identity, teamID, members)
	if err != nil {
		return appservice.Team{}, teamError(meta, err, i18n.ErrorTeamMemberRemoveFailed)
	}
	slog.InfoContext(ctx, "团队成员移出成功", "team_id", teamID, "requested_member_count", len(members))
	return teamFromAction(*team), nil
}

// teamFieldKeys 把团队校验错误码映射为本地化文案键。
var teamFieldKeys = map[common.FieldCode]i18n.Key{
	teamaction.ValidationNameDuplicate: i18n.FieldTeamNameDuplicate,
	teamaction.ValidationQueryInvalid:  i18n.FieldTeamQueryInvalid,
}

// teamErrors 是团队领域的错误转换规则。
var teamErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(teamFieldKeys),
	dispatch.Is(teamaction.ErrNotFound, dispatch.NotFound(i18n.ErrorTeamNotFound)),
	dispatch.Is(teamaction.ErrMemberNotFound, dispatch.NotFound(i18n.ErrorTeamMemberNotFound)),
	dispatch.Is(teamaction.ErrMemberInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
})

// teamError 转换团队领域错误。
func teamError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return teamErrors.Translate(meta, err, failureKey)
}

// teamFromAction 转换团队契约。
func teamFromAction(team teamaction.TeamRecord) appservice.Team {
	return appservice.Team{ID: team.ID, Name: team.Name, Description: team.Description, MemberCount: team.MemberCount, CreatedAt: team.CreatedAt, UpdatedAt: team.UpdatedAt}
}
