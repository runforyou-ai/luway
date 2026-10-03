//go:build server

package direct

import (
	"context"
	"errors"

	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ListMemberOptions 返回可分配的企业身份。
func (o *directOperations) ListMemberOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.MemberOptionListInput) (appservice.MemberOptionList, error) {
	output, err := o.listMemberOptions.Execute(ctx, identity, memberaction.ListOptionsInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if errors.Is(err, memberaction.ErrQueryInvalid) {
		return appservice.MemberOptionList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.MemberOptionList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := make([]string, 0, len(output.Members))
	for _, member := range output.Members {
		if member.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *member.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.MemberOptionList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	members := make([]appservice.MemberOption, 0, len(output.Members))
	for _, member := range output.Members {
		members = append(members, appservice.MemberOption{
			ID: member.ID, Type: appservice.OrganizationIdentityType(member.Type), DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID),
		})
	}
	return appservice.MemberOptionList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// ListColleagues 返回通讯录同事目录。
func (o *directOperations) ListColleagues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ColleagueListInput) (appservice.ColleagueList, error) {
	output, err := o.listColleagues.Execute(ctx, identity, memberaction.ListColleaguesInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if errors.Is(err, memberaction.ErrQueryInvalid) {
		return appservice.ColleagueList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.ColleagueList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := make([]*string, 0, len(output.Colleagues))
	for _, colleague := range output.Colleagues {
		avatarFileIDs = append(avatarFileIDs, colleague.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ColleagueList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	colleagues := make([]appservice.Colleague, 0, len(output.Colleagues))
	for _, colleague := range output.Colleagues {
		teams := make([]appservice.TeamSummary, 0, len(colleague.Teams))
		for _, team := range colleague.Teams {
			teams = append(teams, appservice.TeamSummary{ID: team.ID, Name: team.Name})
		}
		colleagues = append(colleagues, appservice.Colleague{
			IdentityID: colleague.IdentityID, IdentityType: appservice.OrganizationIdentityType(colleague.IdentityType),
			UserID: colleague.UserID, AgentID: colleague.AgentID,
			DisplayName: colleague.DisplayName, AvatarURL: optionalFileURL(avatarURLs, colleague.AvatarFileID),
			WorkStatus: appservice.WorkStatus(colleague.WorkStatus), Email: colleague.Email, ResponsibleName: colleague.ResponsibleName,
			Teams: teams, CreatedAt: colleague.CreatedAt,
		})
	}
	return appservice.ColleagueList{Colleagues: colleagues, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}
