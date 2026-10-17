//go:build server

package direct

import (
	"context"
	"errors"

	knowledgegapaction "github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// serviceIssueOps 持有 AI 表现与团队表现共用的问题会话详情查询。
type serviceIssueOps struct {
	serviceIssue *knowledgegapaction.IssueQuery
	// files 解析文件地址。
	files *fileOps
}

// newServiceIssueOps 创建问题会话详情的业务实现依赖。
func newServiceIssueOps(db *bun.DB, files *fileOps) *serviceIssueOps {
	return &serviceIssueOps{serviceIssue: knowledgegapaction.NewIssueQuery(db), files: files}
}

// GetServiceIssue 返回客服周期的质检结论与对客沟通。
func (o *serviceIssueOps) GetServiceIssue(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, serviceSessionID string) (appservice.ServiceIssueDetail, error) {
	detail, err := o.serviceIssue.Execute(ctx, identity, serviceSessionID)
	if errors.Is(err, knowledgegapaction.ErrIssueNotFound) {
		return appservice.ServiceIssueDetail{}, appservice.NotFoundError(meta, i18n.ErrorServiceIssueNotFound)
	}
	if err != nil {
		return appservice.ServiceIssueDetail{}, serviceIssueError(meta, err)
	}
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, detail.RequesterAvatarFileID)
	if err != nil {
		return appservice.ServiceIssueDetail{}, serviceIssueError(meta, err)
	}
	return appservice.ServiceIssueDetail{Issue: serviceIssueOutput(detail.Issue, avatarURLs), Messages: arr.Map(detail.Messages, serviceTranscriptMessageFromAction)}, nil
}

// serviceIssueError 把问题会话读取失败转换为本地化错误。
func serviceIssueError(meta appservice.RequestMeta, err error) error {
	return appservice.FailedError(meta, i18n.ErrorServiceIssueLoadFailed, err)
}

// serviceIssueList 把一页问题会话转换为传输结构，并批量生成发起人头像地址。
func (o *serviceIssueOps) serviceIssueList(ctx context.Context, identity *servermodels.Identity, list *knowledgegapaction.IssueList) (appservice.ServiceIssueList, error) {
	avatarFileIDs := arr.Map(list.Issues, func(issue knowledgegapaction.Issue) *string { return issue.RequesterAvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ServiceIssueList{}, err
	}
	issues := arr.Map(list.Issues, func(issue knowledgegapaction.Issue) appservice.ServiceIssue {
		return serviceIssueOutput(issue, avatarURLs)
	})
	return appservice.ServiceIssueList{Issues: issues, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// serviceIssueOutput 把问题会话转换为传输结构，头像地址取自已批量生成的文件地址。
func serviceIssueOutput(issue knowledgegapaction.Issue, avatarURLs map[string]string) appservice.ServiceIssue {
	return appservice.ServiceIssue{
		ServiceSessionID: issue.ServiceSessionID, ConversationID: issue.ConversationID, OpeningMessageID: issue.OpeningMessageID,
		ChannelType: (*appservice.ChannelType)(issue.ChannelType), ChannelName: issue.ChannelName,
		RequesterName: support.Deref(issue.RequesterName), RequesterContactNumber: issue.RequesterContactNumber, RequesterAvatarURL: optionalFileURL(avatarURLs, issue.RequesterAvatarFileID),
		ClosedAt: issue.ClosedAt, Summary: issue.Summary, Preview: issue.Preview,
		Satisfaction:      (*appservice.ServiceSessionSatisfaction)(issue.Satisfaction),
		AIIncorrect:       issue.AIIncorrect != nil && *issue.AIIncorrect,
		AIMissedHandoff:   issue.AIMissedHandoff != nil && *issue.AIMissedHandoff,
		AIPoorAttitude:    issue.AIPoorAttitude != nil && *issue.AIPoorAttitude,
		HumanIncorrect:    issue.HumanIncorrect != nil && *issue.HumanIncorrect,
		HumanPoorAttitude: issue.HumanPoorAttitude != nil && *issue.HumanPoorAttitude,
	}
}
