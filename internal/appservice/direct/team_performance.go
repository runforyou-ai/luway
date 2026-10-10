//go:build server

package direct

import (
	"context"

	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// teamPerformanceOps 持有团队表现报表的查询。
type teamPerformanceOps struct {
	teamPerformanceOverview   *teamperformanceaction.OverviewQuery
	teamPerformanceMembers    *teamperformanceaction.MemberListQuery
	teamPerformanceBreakdowns *teamperformanceaction.BreakdownQuery
	teamPerformanceIssues     *teamperformanceaction.IssueListQuery
	// files 解析文件地址。
	files *fileOps
	// serviceIssues 输出服务问题列表。
	serviceIssues *serviceIssueOps
}

// newTeamPerformanceOps 创建团队表现报表的业务实现依赖。
func newTeamPerformanceOps(db *bun.DB, files *fileOps, serviceIssues *serviceIssueOps) *teamPerformanceOps {
	return &teamPerformanceOps{
		teamPerformanceOverview:   teamperformanceaction.NewOverviewQuery(db),
		teamPerformanceMembers:    teamperformanceaction.NewMemberListQuery(db),
		teamPerformanceBreakdowns: teamperformanceaction.NewBreakdownQuery(db),
		teamPerformanceIssues:     teamperformanceaction.NewIssueListQuery(db),
		files:                     files,
		serviceIssues:             serviceIssues,
	}
}

// GetTeamPerformanceReport 返回当前企业指定范围内的真人客服表现概览。
func (o *teamPerformanceOps) GetTeamPerformanceReport(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceReportInput) (appservice.TeamPerformanceReport, error) {
	scope := teamperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, TeamID: input.TeamID, PublicQueue: input.PublicQueue}
	summary, err := o.teamPerformanceOverview.Execute(ctx, identity, scope)
	if err != nil {
		return appservice.TeamPerformanceReport{}, teamPerformanceError(meta, err)
	}
	return appservice.TeamPerformanceReport(*summary), nil
}

// ListTeamPerformanceMembers 返回按客服拆分的一页真人客服表现。
func (o *teamPerformanceOps) ListTeamPerformanceMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceMemberListInput) (appservice.TeamPerformanceMemberList, error) {
	scope := teamperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, TeamID: input.TeamID, PublicQueue: input.PublicQueue}
	list, err := o.teamPerformanceMembers.Execute(ctx, identity, teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamPerformanceMemberList{}, teamPerformanceError(meta, err)
	}
	avatarFileIDs := arr.Map(list.Rows, func(row teamperformanceaction.Member) *string { return row.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.TeamPerformanceMemberList{}, teamPerformanceError(meta, err)
	}
	rows := arr.Map(list.Rows, func(row teamperformanceaction.Member) appservice.TeamPerformanceMember {
		return appservice.TeamPerformanceMember{
			IdentityID: row.IdentityID, DisplayName: row.DisplayName, AvatarURL: optionalFileURL(avatarURLs, row.AvatarFileID),
			Closed: row.Closed, Satisfied: row.Satisfied, SatisfactionJudged: row.SatisfactionJudged, Issues: row.Issues,
		}
	})
	return appservice.TeamPerformanceMemberList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListTeamPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页真人客服表现。
func (o *teamPerformanceOps) ListTeamPerformanceBreakdowns(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceBreakdownInput) (appservice.TeamPerformanceBreakdownList, error) {
	scope := teamperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, TeamID: input.TeamID, PublicQueue: input.PublicQueue}
	list, err := o.teamPerformanceBreakdowns.Execute(ctx, identity, teamperformanceaction.BreakdownInput{
		ListInput: teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize},
		Dimension: input.Dimension,
	})
	if err != nil {
		return appservice.TeamPerformanceBreakdownList{}, teamPerformanceError(meta, err)
	}
	rows := arr.Map(list.Rows, func(row teamperformanceaction.Breakdown) appservice.TeamPerformanceBreakdown {
		return appservice.TeamPerformanceBreakdown{
			ID: support.Deref(row.ID), Name: row.Name, Closed: row.Closed, HumanRequested: row.HumanRequested, FirstResponseMedian: row.FirstResponseMedian,
		}
	})
	return appservice.TeamPerformanceBreakdownList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListTeamPerformanceIssues 返回一页指定类型的真人接待问题会话。
func (o *teamPerformanceOps) ListTeamPerformanceIssues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceIssueListInput) (appservice.ServiceIssueList, error) {
	scope := teamperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, TeamID: input.TeamID, PublicQueue: input.PublicQueue}
	list, err := o.teamPerformanceIssues.Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize},
		Issue:     input.Issue,
	})
	if err != nil {
		return appservice.ServiceIssueList{}, teamPerformanceError(meta, err)
	}
	output, err := o.serviceIssues.serviceIssueList(ctx, identity, list)
	if err != nil {
		return appservice.ServiceIssueList{}, teamPerformanceError(meta, err)
	}
	return output, nil
}

// teamPerformanceErrors 是团队表现查询的错误转换规则。
var teamPerformanceErrors = dispatch.Catalog{
	dispatch.Is(teamperformanceaction.ErrPageSizeInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(teamperformanceaction.ErrDimensionInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(teamperformanceaction.ErrIssueInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
}

// teamPerformanceError 把团队表现查询错误转换为结构化、本地化错误。
func teamPerformanceError(meta appservice.RequestMeta, err error) error {
	return teamPerformanceErrors.Translate(meta, err, i18n.ErrorTeamPerformanceReportLoadFailed)
}
