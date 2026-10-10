//go:build server

package channelinbound

import (
	"context"
	"fmt"
	"log/slog"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// SendNoticeActionName 是经渠道向对方发送提示的任务的 Action 名称。
const SendNoticeActionName = "channel.send_notice"

// SendNoticeInput 是一次渠道提示的发送参数，Recipient 是平台会话编号。
type SendNoticeInput struct {
	WorkspaceID string             `json:"workspaceId"`
	ChannelID   string             `json:"channelId"`
	ChannelType domain.ChannelType `json:"channelType"`
	AccountID   string             `json:"accountId"`
	Recipient   string             `json:"recipient"`
	Body        string             `json:"body"`
}

// SendNoticeAction 经渠道适配器向对方发送提示。
type SendNoticeAction struct {
	db       *bun.DB
	adapters *channeladapter.Registry
}

// NewSendNoticeAction 创建渠道提示发送任务。
func NewSendNoticeAction(db *bun.DB, adapters *channeladapter.Registry) *SendNoticeAction {
	return &SendNoticeAction{db: db, adapters: adapters}
}

// Execute 在渠道仍接待且连接的平台账号未变化时发送提示；平台已接受或无法确认结果时结束，平台限流或暂时不可用时交给任务重试，平台明确拒绝时永久失败。
func (a *SendNoticeAction) Execute(ctx context.Context, input SendNoticeInput) error {
	accepts, err := a.db.NewSelect().TableExpr("channels AS c").
		Where("c.id = ? AND c.workspace_id = ? AND c.provider_account_id = ?", input.ChannelID, input.WorkspaceID, input.AccountID).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Exists(ctx)
	if err != nil || !accepts {
		return err
	}
	target := channeladapter.Target{WorkspaceID: input.WorkspaceID, ChannelID: input.ChannelID, AccountID: input.AccountID, Recipient: input.Recipient}
	result, err := sendNotice(ctx, a.adapters, input.ChannelType, target, input.Body)
	if err != nil || result.Outcome == channeladapter.OutcomeSent || result.Outcome == channeladapter.OutcomeUncertain {
		return err
	}
	return noticeError(ctx, input.ChannelID, result)
}

// sendNotice 经渠道适配器发送一条文本提示；渠道类型不支持外发时返回永久错误。
func sendNotice(ctx context.Context, adapters *channeladapter.Registry, channelType domain.ChannelType, target channeladapter.Target, body string) (channeladapter.Result, error) {
	outbound, ok := channeladapter.Lookup[channeladapter.Outbound](adapters, channelType)
	if !ok {
		return channeladapter.Result{}, servertask.Permanent(fmt.Errorf("channel type %s does not support outbound", channelType))
	}
	item := channeladapter.Item{Body: body}
	sendCtx, cancel := context.WithTimeout(ctx, outbound.SendTimeout(item))
	defer cancel()
	return outbound.Send(sendCtx, target, channeladapter.Request{Item: item}), nil
}

// noticeError 把平台未接受提示的结果转换为任务错误：明确拒绝时记录警告并永久失败，其余交给任务重试。
func noticeError(ctx context.Context, channelID string, result channeladapter.Result) error {
	err := fmt.Errorf("send channel notice: %s", result.Code)
	if result.Outcome == channeladapter.OutcomeFailed {
		slog.WarnContext(ctx, "渠道提示发送失败", "channel_id", channelID, "code", result.Code)
		return servertask.Permanent(err)
	}
	return err
}
