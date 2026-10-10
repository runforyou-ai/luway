//go:build server

package wechat

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// ReplyReleaseTestActionName 是经客服消息接口应答全网发布检测的任务 Action 名称。
const ReplyReleaseTestActionName = "wechat.reply_release_test"

// ReplyReleaseTestInput 是客服消息检测的授权码与回复对象。
type ReplyReleaseTestInput struct {
	AppID             string `json:"appId"`
	OpenID            string `json:"openId"`
	AuthorizationCode string `json:"authorizationCode"`
}

// answerReleaseTest 应答全网发布检测专用测试公众号的消息：文本检测与事件检测返回加密的被动回复，客服消息检测投递经客服消息接口回复的任务并返回空响应，其他消息按约定回复 success。
func (a *ReceiveMessageAction) answerReleaseTest(ctx context.Context, cipher *wechat.Cipher, appID string, message wechat.Message) ([]byte, error) {
	var content string
	switch {
	case message.MsgType == wechat.MessageTypeEvent && message.Event != "":
		content = message.Event + wechat.ReleaseTestEventReplySuffix
	case message.MsgType == wechat.MessageTypeText && message.Content == wechat.ReleaseTestText:
		content = wechat.ReleaseTestText + wechat.ReleaseTestTextReplySuffix
	case message.MsgType == wechat.MessageTypeText:
		code, ok := wechat.ReleaseTestAuthCode(message.Content)
		if !ok {
			return nil, nil
		}
		if err := a.tasks.Enqueue(ctx, ReplyReleaseTestActionName, ReplyReleaseTestInput{AppID: appID, OpenID: message.FromUserName, AuthorizationCode: code}, servertask.EnqueueOptions{
			Queue: servertask.QueueMaintenance, MaxAttempts: 3, IdempotencyKey: "wechat-release-test:" + code,
		}); err != nil {
			return nil, err
		}
		return []byte{}, nil
	default:
		return nil, nil
	}
	// 被动回复的时间戳取本机时钟，随机串每次新生成。
	now := time.Now().Unix() //clock:local
	return cipher.Seal(wechat.TextReply(message.FromUserName, message.ToUserName, now, content), strconv.FormatInt(now, 10), random.Hex(8))
}

// ReplyReleaseTestAction 用授权码换取测试公众号的接口调用凭据，经客服消息接口回复全网发布检测。
type ReplyReleaseTestAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewReplyReleaseTestAction 创建全网发布检测的客服消息应答任务。
func NewReplyReleaseTestAction(db *bun.DB, client *wechat.Client) *ReplyReleaseTestAction {
	return &ReplyReleaseTestAction{db: db, client: client}
}

// Execute 用平台凭据与授权码换取授权方凭据，向检测用户发送授权码加约定后缀的文本；平台未配置时直接结束，微信拒绝请求时记录警告后结束。
func (a *ReplyReleaseTestAction) Execute(ctx context.Context, input ReplyReleaseTestInput) error {
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if errors.Is(err, ErrPlatformNotConfigured) {
		return nil
	}
	if err != nil {
		return err
	}
	componentToken, err := PlatformAccessToken(ctx, a.db, a.client, false)
	if err != nil {
		return err
	}
	authorization, err := a.client.QueryAuthorization(ctx, componentToken, platform.ComponentAppID, input.AuthorizationCode)
	if err == nil {
		err = a.client.SendCustomMessage(ctx, authorization.AccessToken.Value, wechat.CustomMessage{
			ToUser: input.OpenID, Text: input.AuthorizationCode + wechat.ReleaseTestAPIReplySuffix,
		})
	}
	var apiError *wechat.APIError
	if errors.As(err, &apiError) {
		slog.WarnContext(ctx, "全网发布检测客服消息应答被微信拒绝", "app_id", input.AppID, "error", err)
		return nil
	}
	if err == nil {
		slog.InfoContext(ctx, "全网发布检测客服消息已应答", "app_id", input.AppID)
	}
	return err
}
