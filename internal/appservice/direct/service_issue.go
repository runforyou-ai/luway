//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	serviceissueaction "github.com/runforyou-ai/cervi/internal/actions/serviceissue"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// serviceIssueOps 持有 AI 表现与团队表现共用的问题会话详情查询。
type serviceIssueOps struct {
	serviceIssue *serviceissueaction.Query
}

// newServiceIssueOps 创建问题会话详情的业务实现依赖。
func newServiceIssueOps(db *bun.DB) serviceIssueOps {
	return serviceIssueOps{serviceIssue: serviceissueaction.NewQuery(db)}
}

// GetServiceIssue 返回客服周期的质检结论与对客沟通。
func (o *directOperations) GetServiceIssue(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, serviceSessionID string) (appservice.ServiceIssueDetail, error) {
	detail, err := o.serviceIssue.Execute(ctx, identity, serviceSessionID)
	if errors.Is(err, serviceissueaction.ErrNotFound) {
		return appservice.ServiceIssueDetail{}, appservice.NotFoundError(meta, i18n.ErrorServiceIssueNotFound)
	}
	if err != nil {
		return appservice.ServiceIssueDetail{}, serviceIssueError(meta, err, identity.Organization.ID, serviceSessionID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, detail.RequesterAvatarFileID)
	if err != nil {
		return appservice.ServiceIssueDetail{}, serviceIssueError(meta, err, identity.Organization.ID, serviceSessionID)
	}
	output := appservice.ServiceIssueDetail{Issue: serviceIssueOutput(detail.Issue, avatarURLs), Messages: make([]appservice.ServiceTranscriptMessage, 0, len(detail.Messages))}
	for _, message := range detail.Messages {
		output.Messages = append(output.Messages, appservice.ServiceTranscriptMessage{
			ID: message.ID, Type: appservice.MessageType(message.Type), Sender: appservice.ServiceTranscriptSender(message.Sender), SenderName: message.SenderName, Body: message.Body, CreatedAt: message.CreatedAt,
		})
	}
	return output, nil
}

// serviceIssueError 记录问题会话读取失败并返回本地化错误。
func serviceIssueError(meta appservice.RequestMeta, err error, organizationID, serviceSessionID string) error {
	slog.Warn("读取问题会话失败", "organization_id", organizationID, "service_session_id", serviceSessionID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorServiceIssueLoadFailed)
}

// serviceIssueList 把一页问题会话转换为传输结构，并批量生成发起人头像地址。
func (o *directOperations) serviceIssueList(ctx context.Context, identity *servermodels.Identity, list *serviceissueaction.List) (appservice.ServiceIssueList, error) {
	avatarFileIDs := make([]*string, 0, len(list.Issues))
	for _, issue := range list.Issues {
		avatarFileIDs = append(avatarFileIDs, issue.RequesterAvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ServiceIssueList{}, err
	}
	issues := make([]appservice.ServiceIssue, 0, len(list.Issues))
	for _, issue := range list.Issues {
		issues = append(issues, serviceIssueOutput(issue, avatarURLs))
	}
	return appservice.ServiceIssueList{Issues: issues, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// serviceIssueOutput 把问题会话转换为传输结构，头像地址取自已批量生成的文件地址。
func serviceIssueOutput(issue serviceissueaction.Issue, avatarURLs map[string]string) appservice.ServiceIssue {
	return appservice.ServiceIssue{
		ServiceSessionID: issue.ServiceSessionID, ConversationID: issue.ConversationID, OpeningMessageID: issue.OpeningMessageID,
		ChannelType: (*appservice.ChannelType)(issue.ChannelType), ChannelName: issue.ChannelName,
		RequesterName: common.StringValue(issue.RequesterName), RequesterContactNumber: issue.RequesterContactNumber, RequesterAvatarURL: optionalFileURL(avatarURLs, issue.RequesterAvatarFileID),
		ClosedAt: issue.ClosedAt, Summary: issue.Summary, Preview: issue.Preview,
		Satisfaction:      (*appservice.ServiceSessionSatisfaction)(issue.Satisfaction),
		AIIncorrect:       issue.AIIncorrect != nil && *issue.AIIncorrect,
		AIMissedHandoff:   issue.AIMissedHandoff != nil && *issue.AIMissedHandoff,
		AIPoorAttitude:    issue.AIPoorAttitude != nil && *issue.AIPoorAttitude,
		HumanIncorrect:    issue.HumanIncorrect != nil && *issue.HumanIncorrect,
		HumanPoorAttitude: issue.HumanPoorAttitude != nil && *issue.HumanPoorAttitude,
	}
}
