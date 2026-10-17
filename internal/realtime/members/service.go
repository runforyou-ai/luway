//go:build server

// Package members 认证账号、访客与电脑连接并通过应用实时网关 发布实时通知。
package members

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/messaging"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Backend 复用业务入口的账号会话和有效成员解析。
type Backend interface {
	AuthorizeRunChannel(context.Context, string, string, string, string) (bool, error)
}

// Service 持有实时身份认证、频道授权和强制撤销所需的服务依赖。
type Service struct {
	Server *gateway.Server
	db     *bun.DB
}

// New 创建实时服务，连接身份与授权频道来自同一次业务认证。
func New(connection *messaging.Connection, backend Backend, db *bun.DB) (*Service, error) {
	srv := gateway.New(connection)
	srv.Authenticate = func(ctx context.Context, ticket string) (gateway.User, error) {
		if strings.HasPrefix(ticket, "rt_v.") || strings.HasPrefix(ticket, "rt_c.") {
			kind, identity, err := realtimeauth.Authenticate(ctx, db, ticket)
			if err != nil {
				return gateway.User{}, err
			}
			info := appservice.RealtimePeerConnection{UserID: kind + "_" + identity.ID, Path: "/realtime"}
			info.Channel = realtime.VisitorChannel(identity.WorkspaceID, identity.ID)
			if kind == "c" {
				info.Channel = realtime.ComputerChannel(identity.WorkspaceID, identity.ID)
			} else {
				info.TypingChannel = info.Channel + ".typing"
				info.ReceptionChannel = "w." + identity.WorkspaceID + ".visitors"
			}
			channels := []string{info.Channel}
			if kind == "v" {
				channels = append(channels, info.TypingChannel, info.ReceptionChannel)
			}
			return gateway.User{ID: info.UserID, Info: info, Channels: channels}, nil
		}
		account, err := realtimeauth.AuthenticateMember(ctx, db, ticket)
		if err != nil {
			return gateway.User{}, err
		}
		members, err := authaction.ListMemberships(ctx, db, account)
		if err != nil {
			return gateway.User{}, err
		}
		info := appservice.RealtimeConnection{UserID: "a_" + account.Account.ID, SessionID: account.Session.ID, Path: "/realtime", Members: make([]appservice.RealtimeMember, 0, len(members))}
		var channels []string
		for _, member := range members {
			ch := direct.RealtimeMemberChannels(member.WorkspaceID, member.UserID)
			info.Members = append(info.Members, ch)
			channels = append(channels, ch.Channel, ch.InboxChannel, ch.TypingChannel, ch.InboxTypingChannel)
		}
		return gateway.User{ID: info.UserID, Session: info.SessionID, Info: info, Channels: channels}, nil
	}
	srv.Authorize = func(ctx context.Context, user gateway.User, channel string) (bool, error) {
		parts := strings.Split(channel, ".")
		account, ok := strings.CutPrefix(user.ID, "a_")
		if !ok || len(parts) != 4 || parts[0] != "w" || parts[2] != "runs" {
			return false, nil
		}
		return backend.AuthorizeRunChannel(ctx, account, user.Session, parts[1], parts[3])
	}
	return &Service{Server: srv, db: db}, nil
}

// Start 订阅撤销控制并开始认证客户端。
func (s *Service) Start(ctx context.Context) error {
	if err := s.Server.Start(ctx); err != nil {
		return errors.Join(err, s.Server.Close())
	}
	return nil
}

// Stop 停止实时服务。
func (s *Service) Stop() error { return s.Server.Close() }

// Publish 将业务通知转换为实时事件契约并发送至精确私有频道。
func (s *Service) Publish(ctx context.Context, notification realtime.Notification) error {
	if notification.AudienceKind != realtime.AudienceUser && notification.AudienceKind != realtime.AudienceCustomerInbox && notification.AudienceKind != realtime.AudienceAccount {
		return s.publishPeer(ctx, notification)
	}
	if notification.AudienceKind == realtime.AudienceAccount || realtime.IsAuthorizationChange(notification.Kind) {
		return s.reauthorize(ctx, notification)
	}
	var revocationErr error
	if notification.Kind == realtime.KindConversationRemoved {
		revocationErr = s.reauthorize(ctx, notification)
	}
	frame := NotificationFrame(notification)
	if frame == nil {
		return nil
	}
	channel := realtime.MemberChannels(notification.WorkspaceID, notification.AudienceID).Channel
	if notification.AudienceKind == realtime.AudienceCustomerInbox {
		channel = "w." + notification.WorkspaceID + ".inbox"
	}
	if notification.Kind == realtime.KindConversationTyping {
		channel += ".typing"
	}
	return errors.Join(revocationErr, s.broadcast(ctx, frame, notification.ID, channel))
}

// reauthorize 按数据库中的账号归属强制关闭连接，客户端复核会话并获取当前成员授权。
func (s *Service) reauthorize(ctx context.Context, notification realtime.Notification) error {
	accountID := notification.AudienceID
	if notification.AudienceKind != realtime.AudienceAccount {
		if err := s.db.NewSelect().Model((*servermodels.User)(nil)).Column("account_id").Where("id = ? AND workspace_id = ?", notification.AudienceID, notification.WorkspaceID).Scan(ctx, &accountID); err != nil {
			return err
		}
	}
	target := gateway.Target{User: "a_" + accountID, ID: notification.ID}
	if notification.TokenSessionID != "" {
		target.Session = notification.TokenSessionID
	}
	err := s.Server.Disconnect(ctx, target)
	return errors.Join(err, s.revokeInvalidComputers(ctx, accountID, notification.ID))
}

// PublishRun 将运行状态投影为留存事件，大事件明确要求重新读取快照。
func (s *Service) PublishRun(ctx context.Context, workspaceID string, event realtime.RunStreamEvent) error {
	var frame protocol.Frame = protocol.RunStreamInvalidated{RunID: event.RunID, StreamID: event.StreamID, Attempt: event.Attempt, Sequence: event.Sequence}
	if event.Delta != nil {
		frame = protocol.RunStreamDeltaFrame(event.RunID, event.Attempt, *event.Delta)
	}
	data, err := protocol.Encode(frame)
	if err != nil {
		return err
	}
	if len(data) > 256*1024 {
		frame = protocol.RunStreamInvalidated{RunID: event.RunID, StreamID: event.StreamID, Attempt: event.Attempt, Sequence: event.Sequence}
		data, err = protocol.Encode(frame)
		if err != nil {
			return err
		}
	}
	channel := []string{realtime.RunChannel(workspaceID, event.RunID)}
	// 结束批次没有增量也无需重读快照时只发送结束帧。
	if event.Delta != nil || event.Invalidated || !event.Ended {
		err = s.Server.Broadcast(ctx, gateway.Event{Name: string(frame.FrameType()), Channels: channel, Data: json.RawMessage(data)})
	}
	if !event.Ended {
		return err
	}
	// 结束帧独立发送，增量帧失败时仍然通知客户端结束。
	endErr := s.broadcast(ctx, protocol.RunStreamEnded{RunID: event.RunID}, "", channel...)
	return errors.Join(err, endErr)
}

// broadcast 编码业务事件并向指定私有频道发布。
func (s *Service) broadcast(ctx context.Context, frame protocol.Frame, id string, channels ...string) error {
	data, err := protocol.Encode(frame)
	if err != nil {
		return err
	}
	err = s.Server.Broadcast(ctx, gateway.Event{ID: id, Name: string(frame.FrameType()), Channels: channels, Data: data})
	return err
}
