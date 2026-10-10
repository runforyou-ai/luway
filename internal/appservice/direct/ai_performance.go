//go:build server

package direct

import (
	"context"

	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// aiPerformanceOps 持有 AI 表现报表、问题会话与 AI 员工服务记录的查询。
type aiPerformanceOps struct {
	aiPerformanceOverview    *aiperformanceaction.OverviewQuery
	aiPerformanceBreakdowns  *aiperformanceaction.BreakdownQuery
	agentServiceSessionsList *aiperformanceaction.ServiceSessionListQuery
	aiPerformanceIssues      *aiperformanceaction.IssueListQuery
	// files 解析文件地址。
	files *fileOps
	// serviceIssues 输出服务问题列表。
	serviceIssues *serviceIssueOps
}

// newAIPerformanceOps 创建 AI 表现报表的业务实现依赖。
func newAIPerformanceOps(db *bun.DB, files *fileOps, serviceIssues *serviceIssueOps) *aiPerformanceOps {
	return &aiPerformanceOps{
		aiPerformanceOverview:    aiperformanceaction.NewOverviewQuery(db),
		aiPerformanceBreakdowns:  aiperformanceaction.NewBreakdownQuery(db),
		agentServiceSessionsList: aiperformanceaction.NewServiceSessionListQuery(db),
		aiPerformanceIssues:      aiperformanceaction.NewIssueListQuery(db),
		files:                    files,
		serviceIssues:            serviceIssues,
	}
}

// GetAIPerformanceReport 返回当前企业指定范围内的 AI 客服表现概览。
func (o *aiPerformanceOps) GetAIPerformanceReport(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceReportInput) (appservice.AIPerformanceReport, error) {
	agents := reportAgentScope(identity, input.AgentID, input.Mine)
	overview, err := o.aiPerformanceOverview.Execute(ctx, identity, aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents})
	if err != nil {
		return appservice.AIPerformanceReport{}, aiPerformanceError(meta, err)
	}
	return appservice.AIPerformanceReport{
		Summary: appservice.AIPerformanceSummary(overview.Summary),
		HandoffReasons: arr.Map(overview.HandoffReasons, func(reason aiperformanceaction.ReasonCount) appservice.AIHandoffReasonCount {
			return appservice.AIHandoffReasonCount{Reason: appservice.AgentHandoffReason(reason.Reason), Count: reason.Count}
		}),
		KnowledgeGapTotal: overview.KnowledgeGapTotal,
	}, nil
}

// ListAIPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页 AI 客服表现。
func (o *aiPerformanceOps) ListAIPerformanceBreakdowns(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceBreakdownInput) (appservice.AIPerformanceBreakdownList, error) {
	agents := reportAgentScope(identity, input.AgentID, input.Mine)
	list, err := o.aiPerformanceBreakdowns.Execute(ctx, identity, aiperformanceaction.BreakdownInput{
		Input:     aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents},
		Dimension: input.Dimension, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.AIPerformanceBreakdownList{}, aiPerformanceError(meta, err)
	}
	rows := arr.Map(list.Rows, func(row aiperformanceaction.Breakdown) appservice.AIPerformanceBreakdown {
		return appservice.AIPerformanceBreakdown{ID: support.Deref(row.ID), Name: row.Name, Closed: row.Closed, Resolved: row.Resolved, AIResolved: row.AIResolved}
	})
	return appservice.AIPerformanceBreakdownList{Rows: rows, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// ListAIPerformanceIssues 返回一页指定类型的 AI 表现问题会话。
func (o *aiPerformanceOps) ListAIPerformanceIssues(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIPerformanceIssueListInput) (appservice.ServiceIssueList, error) {
	agents := reportAgentScope(identity, input.AgentID, input.Mine)
	list, err := o.aiPerformanceIssues.Execute(ctx, identity, aiperformanceaction.IssueListInput{
		Input: aiperformanceaction.Input{Days: input.Days, ChannelID: input.ChannelID, Agents: agents},
		Issue: input.Issue, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.ServiceIssueList{}, aiPerformanceError(meta, err)
	}
	output, err := o.serviceIssues.serviceIssueList(ctx, identity, list)
	if err != nil {
		return appservice.ServiceIssueList{}, aiPerformanceError(meta, err)
	}
	return output, nil
}

// ListAgentServiceSessions 返回 AI 员工接待的一页服务周期。
func (o *aiPerformanceOps) ListAgentServiceSessions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.AgentServiceSessionListInput) (appservice.AgentServiceSessionList, error) {
	list, err := o.agentServiceSessionsList.Execute(ctx, identity, aiperformanceaction.ServiceSessionListInput{AgentID: agentID, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.AgentServiceSessionList{}, agentServiceSessionsError(meta, err)
	}
	avatarFileIDs := arr.Map(list.Sessions, func(session aiperformanceaction.ServiceSession) *string { return session.RequesterAvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.AgentServiceSessionList{}, agentServiceSessionsError(meta, err)
	}
	sessions := arr.Map(list.Sessions, func(session aiperformanceaction.ServiceSession) appservice.AgentServiceSession {
		return appservice.AgentServiceSession{
			ServiceSessionID: session.ID, ConversationID: session.ConversationID, OpeningMessageID: session.OpeningMessageID,
			Source: appservice.ServiceSource(session.Source), Audience: appservice.ServiceAudience(session.Audience),
			ChannelType: (*appservice.ChannelType)(session.ChannelType), ChannelName: session.ChannelName,
			RequesterName: support.Deref(session.RequesterName), RequesterContactNumber: session.RequesterContactNumber, RequesterAvatarURL: optionalFileURL(avatarURLs, session.RequesterAvatarFileID),
			Status: appservice.ServiceSessionStatus(session.Status), OpenedAt: session.OpenedAt, ClosedAt: session.ClosedAt,
			CloseReason: (*appservice.ServiceSessionCloseReason)(session.CloseReason), Preview: session.Preview, Summary: session.Summary, Resolved: session.Resolved,
		}
	})
	return appservice.AgentServiceSessionList{Sessions: sessions, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// reportAgentScope 返回报表与待补知识的 AI 员工范围；mine 限定为当前成员负责的 AI 员工。
func reportAgentScope(identity *servermodels.Identity, agentID string, mine bool) reportpage.AgentScope {
	scope := reportpage.AgentScope{AgentID: agentID}
	if mine {
		scope.ResponsibleUserID = identity.User.ID
	}
	return scope
}

// agentServiceSessionsError 把服务记录查询错误转换为结构化、本地化错误。
func agentServiceSessionsError(meta appservice.RequestMeta, err error) error {
	return dispatch.Catalog{
		dispatch.Is(aiperformanceaction.ErrPageSizeInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	}.Translate(meta, err, i18n.ErrorAgentServiceSessionsLoadFailed)
}

// aiPerformanceErrors 是 AI 表现报表查询的错误转换规则。
var aiPerformanceErrors = dispatch.Catalog{
	dispatch.Is(aiperformanceaction.ErrPageSizeInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(aiperformanceaction.ErrDimensionInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(aiperformanceaction.ErrIssueInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
}

// aiPerformanceError 把报表查询错误转换为结构化、本地化错误。
func aiPerformanceError(meta appservice.RequestMeta, err error) error {
	return aiPerformanceErrors.Translate(meta, err, i18n.ErrorAIPerformanceReportLoadFailed)
}
