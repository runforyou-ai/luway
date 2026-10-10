//go:build server

package channelinbound

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
)

// ReceiveEventActionName 是渠道入站事件处理任务的 Action 名称。
const ReceiveEventActionName = "channel.receive_event"

// ReceiveEventInput 是一次渠道入站事件的处理参数，AccountID 是推送事件的平台账号。
type ReceiveEventInput struct {
	WorkspaceID string                      `json:"workspaceId"`
	ChannelID   string                      `json:"channelId"`
	AccountID   string                      `json:"accountId"`
	Event       channeladapter.InboundEvent `json:"event"`
	// Assertion 是推送请求自身给出的发送者核验结论，为空时渠道身份的核验由渠道身份断言维护。
	Assertion *Assertion `json:"assertion,omitempty"`
}

// Assertion 是业务系统转发消息时经签名身份给出的发送者核验结论，UserID 为空表示发送者未核验。
type Assertion struct {
	UserID  string                       `json:"userId,omitempty"`
	Email   string                       `json:"email,omitempty"`
	Profile *domain.SignedContactProfile `json:"profile,omitempty"`
}

// EnqueueEvent 把平台推送的入站事件写入任务队列，按渠道、平台账号与平台事件编号排重；route 非空时事件只由持有该任务路由租约的服务端实例处理。
func EnqueueEvent(ctx context.Context, tasks servertask.Enqueuer, input ReceiveEventInput, route string) error {
	err := tasks.Enqueue(ctx, ReceiveEventActionName, input, servertask.EnqueueOptions{
		WorkspaceID: input.WorkspaceID, Route: route, IdempotencyKey: "chevt:" + input.ChannelID + ":" + input.AccountID + ":" + input.Event.ID,
	})
	return err
}
