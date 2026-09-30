//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	aiperformanceaction "github.com/runforyou-ai/cervi/internal/actions/aiperformance"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// aiPerformanceOps 持有 AI 表现报表、问题会话与 AI 员工服务记录的查询。
type aiPerformanceOps struct {
	aiPerformanceOverview    *aiperformanceaction.OverviewQuery
	aiPerformanceBreakdowns  *aiperformanceaction.BreakdownQuery
	agentServiceSessionsList *aiperformanceaction.ServiceSessionListQuery
	aiPerformanceIssues      *aiperformanceaction.IssueListQuery
}

// newAIPerformanceOps 创建 AI 表现报表的业务实现依赖。
func newAIPerformanceOps(db *bun.DB) aiPerformanceOps {
	return aiPerformanceOps{
		aiPerformanceOverview:    aiperformanceaction.NewOverviewQuery(db),
		aiPerformanceBreakdowns:  aiperformanceaction.NewBreakdownQuery(db),
		agentServiceSessionsList: aiperformanceaction.NewServiceSessionListQuery(db),
		aiPerformanceIssues:      aiperformanceaction.NewIssueListQuery(db),
	}
}

// GetAIPerformanceReport 返回当前企业指定范围内的 AI 客服表现概览。
func (o *directOperations) GetAIPerformanceReport(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceReportInput) (appservice.AIPerformanceReport, error) {
	agents, err := reportAgentScope(meta, identity, input.ChannelID, input.AgentID, input.Mine)
	if err != nil {
		return appservice.AIPerformanceReport{}, err
	}
	overview, err := o.aiPerformanceOverview.Execute(ctx, identity, aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents})
	if err != nil {
		return appservice.AIPerformanceReport{}, aiPerformanceError(meta, err, identity.Organization.ID)
	}
	output := appservice.AIPerformanceReport{
		Summary:           appservice.AIPerformanceSummary(overview.Summary),
		HandoffReasons:    make([]appservice.AIHandoffReasonCount, 0, len(overview.HandoffReasons)),
		KnowledgeGapTotal: overview.KnowledgeGapTotal,
	}
	for _, reason := range overview.HandoffReasons {
		output.HandoffReasons = append(output.HandoffReasons, appservice.AIHandoffReasonCount{Reason: appservice.AgentHandoffReason(reason.Reason), Count: reason.Count})
	}
	return output, nil
}

// ListAIPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页 AI 客服表现。
func (o *directOperations) ListAIPerformanceBreakdowns(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceBreakdownInput) (appservice.AIPerformanceBreakdownList, error) {
	agents, err := reportAgentScope(meta, identity, input.ChannelID, input.AgentID, input.Mine)
	if err != nil {
		return appservice.AIPerformanceBreakdownList{}, err
	}
	list, err := o.aiPerformanceBreakdowns.Execute(ctx, identity, aiperformanceaction.BreakdownInput{
		Input:     aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents},
		Dimension: domain.ServiceReportDimension(input.Dimension), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.AIPerformanceBreakdownList{}, aiPerformanceError(meta, err, identity.Organization.ID)
	}
	rows := make([]appservice.AIPerformanceBreakdown, 0, len(list.Rows))
	for _, row := range list.Rows {
		rows = append(rows, appservice.AIPerformanceBreakdown{ID: common.StringValue(row.ID), Name: row.Name, Closed: row.Closed, Resolved: row.Resolved, AIResolved: row.AIResolved})
	}
	return appservice.AIPerformanceBreakdownList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListAIPerformanceIssues 返回一页指定类型的 AI 表现问题会话。
func (o *directOperations) ListAIPerformanceIssues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceIssueListInput) (appservice.ServiceIssueList, error) {
	agents, err := reportAgentScope(meta, identity, input.ChannelID, input.AgentID, input.Mine)
	if err != nil {
		return appservice.ServiceIssueList{}, err
	}
	list, err := o.aiPerformanceIssues.Execute(ctx, identity, aiperformanceaction.IssueListInput{
		Input: aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents},
		Issue: domain.ServiceIssueType(input.Issue), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.ServiceIssueList{}, aiPerformanceError(meta, err, identity.Organization.ID)
	}
	output, err := o.serviceIssueList(ctx, identity, list)
	if err != nil {
		return appservice.ServiceIssueList{}, aiPerformanceError(meta, err, identity.Organization.ID)
	}
	return output, nil
}

// ListAgentServiceSessions 返回 AI 员工接待的一页服务周期。
func (o *directOperations) ListAgentServiceSessions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.AgentServiceSessionListInput) (appservice.AgentServiceSessionList, error) {
	if !common.ValidUUID(agentID) {
		return appservice.AgentServiceSessionList{}, appservice.NotFoundError(meta, i18n.ErrorAgentNotFound)
	}
	list, err := o.agentServiceSessionsList.Execute(ctx, identity, aiperformanceaction.ServiceSessionListInput{AgentID: agentID, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.AgentServiceSessionList{}, agentServiceSessionsError(meta, err, identity.Organization.ID, agentID)
	}
	avatarFileIDs := make([]*string, 0, len(list.Sessions))
	for _, session := range list.Sessions {
		avatarFileIDs = append(avatarFileIDs, session.RequesterAvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.AgentServiceSessionList{}, agentServiceSessionsError(meta, err, identity.Organization.ID, agentID)
	}
	sessions := make([]appservice.AgentServiceSession, 0, len(list.Sessions))
	for _, session := range list.Sessions {
		sessions = append(sessions, appservice.AgentServiceSession{
			ServiceSessionID: session.ID, ConversationID: session.ConversationID, OpeningMessageID: session.OpeningMessageID,
			Source: appservice.ServiceSource(session.Source), Audience: appservice.ServiceAudience(session.Audience),
			ChannelType: (*appservice.ChannelType)(session.ChannelType), ChannelName: session.ChannelName,
			RequesterName: common.StringValue(session.RequesterName), RequesterContactNumber: session.RequesterContactNumber, RequesterAvatarURL: optionalFileURL(avatarURLs, session.RequesterAvatarFileID),
			Status: appservice.ServiceSessionStatus(session.Status), OpenedAt: session.OpenedAt, ClosedAt: session.ClosedAt,
			CloseReason: (*appservice.ServiceSessionCloseReason)(session.CloseReason), Preview: session.Preview, Summary: session.Summary, Resolved: session.Resolved,
		})
	}
	return appservice.AgentServiceSessionList{Sessions: sessions, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// reportAgentScope 校验报表与待补知识的渠道和 AI 员工筛选编号，并返回 AI 员工范围；mine 限定为当前成员负责的 AI 员工。
func reportAgentScope(meta appservice.RequestMeta, identity *servermodels.Identity, channelID, agentID string, mine bool) (identityaction.AgentScope, error) {
	if channelID != "" && !common.ValidUUID(channelID) {
		return identityaction.AgentScope{}, appservice.NotFoundError(meta, i18n.ErrorChannelNotFound)
	}
	if agentID != "" && !common.ValidUUID(agentID) {
		return identityaction.AgentScope{}, appservice.NotFoundError(meta, i18n.ErrorAgentNotFound)
	}
	scope := identityaction.AgentScope{AgentID: agentID}
	if mine {
		scope.ResponsibleUserID = identity.User.ID
	}
	return scope, nil
}

// agentServiceSessionsError 把服务记录查询错误转换为结构化、本地化错误。
func agentServiceSessionsError(meta appservice.RequestMeta, err error, organizationID, agentID string) error {
	if errors.Is(err, aiperformanceaction.ErrPageSizeInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	slog.Warn("读取 AI 员工服务记录失败", "organization_id", organizationID, "agent_id", agentID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorAgentServiceSessionsLoadFailed)
}

// aiPerformanceError 把报表查询错误转换为结构化、本地化错误。
func aiPerformanceError(meta appservice.RequestMeta, err error, organizationID string) error {
	if errors.Is(err, aiperformanceaction.ErrPageSizeInvalid) || errors.Is(err, aiperformanceaction.ErrDimensionInvalid) || errors.Is(err, aiperformanceaction.ErrIssueInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	slog.Warn("读取 AI 表现报表失败", "organization_id", organizationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorAIPerformanceReportLoadFailed)
}
