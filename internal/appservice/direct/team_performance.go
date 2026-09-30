//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// teamPerformanceOps 持有团队表现报表的查询。
type teamPerformanceOps struct {
	teamPerformanceOverview   *teamperformanceaction.OverviewQuery
	teamPerformanceMembers    *teamperformanceaction.MemberListQuery
	teamPerformanceBreakdowns *teamperformanceaction.BreakdownQuery
	teamPerformanceIssues     *teamperformanceaction.IssueListQuery
}

// newTeamPerformanceOps 创建团队表现报表的业务实现依赖。
func newTeamPerformanceOps(db *bun.DB) teamPerformanceOps {
	return teamPerformanceOps{
		teamPerformanceOverview:   teamperformanceaction.NewOverviewQuery(db),
		teamPerformanceMembers:    teamperformanceaction.NewMemberListQuery(db),
		teamPerformanceBreakdowns: teamperformanceaction.NewBreakdownQuery(db),
		teamPerformanceIssues:     teamperformanceaction.NewIssueListQuery(db),
	}
}

// GetTeamPerformanceReport 返回当前企业指定范围内的真人客服表现概览。
func (o *directOperations) GetTeamPerformanceReport(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceReportInput) (appservice.TeamPerformanceReport, error) {
	scope, err := teamPerformanceScope(meta, input.Days, input.ChannelID, input.TeamID, input.PublicQueue)
	if err != nil {
		return appservice.TeamPerformanceReport{}, err
	}
	summary, err := o.teamPerformanceOverview.Execute(ctx, identity, scope)
	if err != nil {
		return appservice.TeamPerformanceReport{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	return appservice.TeamPerformanceReport(*summary), nil
}

// ListTeamPerformanceMembers 返回按客服拆分的一页真人客服表现。
func (o *directOperations) ListTeamPerformanceMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceMemberListInput) (appservice.TeamPerformanceMemberList, error) {
	scope, err := teamPerformanceScope(meta, input.Days, input.ChannelID, input.TeamID, input.PublicQueue)
	if err != nil {
		return appservice.TeamPerformanceMemberList{}, err
	}
	list, err := o.teamPerformanceMembers.Execute(ctx, identity, teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.TeamPerformanceMemberList{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	avatarFileIDs := make([]*string, 0, len(list.Rows))
	for _, row := range list.Rows {
		avatarFileIDs = append(avatarFileIDs, row.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.TeamPerformanceMemberList{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	rows := make([]appservice.TeamPerformanceMember, 0, len(list.Rows))
	for _, row := range list.Rows {
		rows = append(rows, appservice.TeamPerformanceMember{
			IdentityID: row.IdentityID, DisplayName: row.DisplayName, AvatarURL: optionalFileURL(avatarURLs, row.AvatarFileID),
			Closed: row.Closed, Satisfied: row.Satisfied, SatisfactionJudged: row.SatisfactionJudged, Issues: row.Issues,
		})
	}
	return appservice.TeamPerformanceMemberList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListTeamPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页真人客服表现。
func (o *directOperations) ListTeamPerformanceBreakdowns(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceBreakdownInput) (appservice.TeamPerformanceBreakdownList, error) {
	scope, err := teamPerformanceScope(meta, input.Days, input.ChannelID, input.TeamID, input.PublicQueue)
	if err != nil {
		return appservice.TeamPerformanceBreakdownList{}, err
	}
	list, err := o.teamPerformanceBreakdowns.Execute(ctx, identity, teamperformanceaction.BreakdownInput{
		ListInput: teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize},
		Dimension: domain.ServiceReportDimension(input.Dimension),
	})
	if err != nil {
		return appservice.TeamPerformanceBreakdownList{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	rows := make([]appservice.TeamPerformanceBreakdown, 0, len(list.Rows))
	for _, row := range list.Rows {
		rows = append(rows, appservice.TeamPerformanceBreakdown{
			ID: common.StringValue(row.ID), Name: row.Name, Closed: row.Closed, HumanRequested: row.HumanRequested, FirstResponseMedian: row.FirstResponseMedian,
		})
	}
	return appservice.TeamPerformanceBreakdownList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListTeamPerformanceIssues 返回一页指定类型的真人接待问题会话。
func (o *directOperations) ListTeamPerformanceIssues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TeamPerformanceIssueListInput) (appservice.ServiceIssueList, error) {
	scope, err := teamPerformanceScope(meta, input.Days, input.ChannelID, input.TeamID, input.PublicQueue)
	if err != nil {
		return appservice.ServiceIssueList{}, err
	}
	list, err := o.teamPerformanceIssues.Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope, Page: input.Page, PageSize: input.PageSize},
		Issue:     domain.ServiceIssueType(input.Issue),
	})
	if err != nil {
		return appservice.ServiceIssueList{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	output, err := o.serviceIssueList(ctx, identity, list)
	if err != nil {
		return appservice.ServiceIssueList{}, teamPerformanceError(meta, err, identity.Organization.ID)
	}
	return output, nil
}

// teamPerformanceScope 校验渠道与团队编号并返回统计范围。
func teamPerformanceScope(meta appservice.RequestMeta, days int, channelID, teamID string, publicQueue bool) (teamperformanceaction.Input, error) {
	if channelID != "" && !common.ValidUUID(channelID) {
		return teamperformanceaction.Input{}, appservice.NotFoundError(meta, i18n.ErrorChannelNotFound)
	}
	if teamID != "" && !common.ValidUUID(teamID) {
		return teamperformanceaction.Input{}, appservice.NotFoundError(meta, i18n.ErrorTeamNotFound)
	}
	return teamperformanceaction.Input{Days: days, ChannelID: channelID, TeamID: teamID, PublicQueue: publicQueue}, nil
}

// teamPerformanceError 把团队表现查询错误转换为结构化、本地化错误。
func teamPerformanceError(meta appservice.RequestMeta, err error, organizationID string) error {
	if errors.Is(err, teamperformanceaction.ErrPageSizeInvalid) || errors.Is(err, teamperformanceaction.ErrDimensionInvalid) || errors.Is(err, teamperformanceaction.ErrIssueInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	slog.Warn("读取团队表现报表失败", "organization_id", organizationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorTeamPerformanceReportLoadFailed)
}
