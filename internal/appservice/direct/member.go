//go:build server

package direct

import (
	"context"
	"errors"

	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// ListMemberOptions 返回可分配的企业身份。
func (o *directoryOps) ListMemberOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.MemberOptionListInput) (appservice.MemberOptionList, error) {
	output, err := o.listMemberOptions.Execute(ctx, identity, memberaction.ListOptionsInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if errors.Is(err, memberaction.ErrQueryInvalid) {
		return appservice.MemberOptionList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.MemberOptionList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := arr.FilterMap(output.Members, func(member memberaction.Option) (string, bool) {
		return support.Deref(member.AvatarFileID), member.AvatarFileID != nil
	})
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.MemberOptionList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	members := arr.Map(output.Members, func(member memberaction.Option) appservice.MemberOption {
		return appservice.MemberOption{
			ID: member.ID, Type: member.Type, DisplayName: member.DisplayName, AvatarURL: optionalFileURL(avatarURLs, member.AvatarFileID),
		}
	})
	return appservice.MemberOptionList{Members: members, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// ListColleagues 返回通讯录同事目录。
func (o *directoryOps) ListColleagues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ColleagueListInput) (appservice.ColleagueList, error) {
	output, err := o.listColleagues.Execute(ctx, identity, memberaction.ListColleaguesInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if errors.Is(err, memberaction.ErrQueryInvalid) {
		return appservice.ColleagueList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.ColleagueList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := arr.Map(output.Colleagues, func(colleague memberaction.Colleague) *string { return colleague.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ColleagueList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	colleagues := arr.Map(output.Colleagues, func(colleague memberaction.Colleague) appservice.Colleague {
		return appservice.Colleague{
			IdentityID: colleague.IdentityID, IdentityType: colleague.IdentityType,
			UserID: colleague.UserID, AgentID: colleague.AgentID,
			DisplayName: colleague.DisplayName, AvatarURL: optionalFileURL(avatarURLs, colleague.AvatarFileID),
			WorkStatus: colleague.WorkStatus, Email: colleague.Email, ResponsibleName: colleague.ResponsibleName,
			Teams: arr.Map(colleague.Teams, func(team teamaction.Summary) appservice.TeamSummary {
				return appservice.TeamSummary{ID: team.ID, Name: team.Name}
			}),
			CreatedAt: colleague.CreatedAt,
		}
	})
	return appservice.ColleagueList{Colleagues: colleagues, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}
