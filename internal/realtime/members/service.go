//go:build server

// Package members 认证账号、访客与电脑连接并通过 jetcast 发布实时通知。
package members

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/runforyou-ai/jetcast"
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Backend 复用业务入口的账号会话和有效成员解析。
type Backend interface {
	AuthenticateAccountMembers(context.Context, appservice.RequestMeta) (direct.AccountMembersSession, error)
	AuthorizeRunChannel(context.Context, string, string, string, string) (bool, error)
}

// Service 持有实时身份认证、频道授权和强制撤销所需的服务依赖。
type Service struct {
	Server *jetcast.Server
	db     *bun.DB
}

// New 创建实时服务，连接身份与授权频道来自同一次业务认证。
func New(connection *broker.Connection, backend Backend, db *bun.DB, prefix string, replicas int) (*Service, error) {
	srv, err := jetcast.NewServer(connection.Conn, jetcast.ServerOptions{
		Account: broker.ClientAccount, CalloutSigner: connection.Signer, Admin: connection.Admin, ManageStreams: true,
		Config:  jetcast.Config{Prefix: prefix, Stream: prefix + "_events", Ephemeral: []string{"w.*.u.*.typing", "w.*.inbox.typing", "w.*.v.*.typing"}},
		History: jetcast.History{Replicas: replicas}, MaxConnectionTTL: broker.MaxConnectionTTL,
	})
	if err != nil {
		return nil, err
	}
	srv.Authenticate(func(ctx context.Context, request jetcast.AuthRequest) (jetcast.User, error) {
		started := time.Now() //clock:local
		if strings.HasPrefix(request.Token, "rt_") {
			kind, identity, err := realtimeauth.Authenticate(ctx, db, request.Token)
			if err != nil {
				return jetcast.User{}, err
			}
			channel := realtime.VisitorChannel(identity.WorkspaceID, identity.ID)
			info := appservice.RealtimePeerConnection{UserID: kind + "_" + identity.ID, Prefix: prefix, Path: "/nats"}
			if kind == "c" {
				channel = realtime.ComputerChannel(identity.WorkspaceID, identity.ID)
			} else {
				info.TypingChannel = channel + ".typing"
				info.ReceptionChannel = "w." + identity.WorkspaceID + ".visitors"
			}
			info.Channel = channel
			return jetcast.User{ID: info.UserID, ExpiresAt: identity.ExpiresAt, Info: info}, nil
		}
		session, err := backend.AuthenticateAccountMembers(ctx, appservice.RequestMeta{Token: request.Token})
		if err != nil {
			return jetcast.User{}, err
		}
		info := appservice.RealtimeConnection{UserID: "a_" + session.AccountID, SessionID: session.SessionID, Prefix: prefix, Path: "/nats", Members: make([]appservice.RealtimeMember, 0, len(session.Members))}
		for _, member := range session.Members {
			info.Members = append(info.Members, direct.RealtimeMemberChannels(member.WorkspaceID, member.UserID))
		}
		return jetcast.User{ID: info.UserID, Session: session.SessionID, ExpiresAt: started.Add(session.Remaining), Info: info}, nil
	})
	srv.Grants(func(_ context.Context, user jetcast.User) ([]string, error) {
		if strings.HasPrefix(user.ID, "v_") || strings.HasPrefix(user.ID, "c_") {
			var info appservice.RealtimePeerConnection
			data, err := json.Marshal(user.Info)
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(data, &info); err != nil {
				return nil, err
			}
			channels := []string{info.Channel}
			if info.TypingChannel != "" {
				channels = append(channels, info.TypingChannel)
			}
			if info.ReceptionChannel != "" {
				channels = append(channels, info.ReceptionChannel)
			}
			return channels, nil
		}
		var info appservice.RealtimeConnection
		data, err := json.Marshal(user.Info)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(data, &info) != nil {
			return nil, errors.New("missing member authorization")
		}
		channels := make([]string, 0, len(info.Members)*4)
		for _, member := range info.Members {
			channels = append(channels, member.Channel, member.InboxChannel, member.TypingChannel, member.InboxTypingChannel)
		}
		return channels, nil
	})
	if err := srv.Channel("w.{workspace}.runs.{run}", func(ctx context.Context, user jetcast.User, params jetcast.Params) (bool, error) {
		accountID, ok := strings.CutPrefix(user.ID, "a_")
		if !ok {
			return false, nil
		}
		return backend.AuthorizeRunChannel(ctx, accountID, user.Session, params["workspace"], params["run"])
	}); err != nil {
		return nil, err
	}
	return &Service{Server: srv, db: db}, nil
}

// Start 创建实时历史与连接登记并开始认证客户端。
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
	data, err := protocol.Encode(frame)
	if err != nil {
		return err
	}
	_, err = s.Server.Broadcast(ctx, jetcast.Event{Name: string(frame.FrameType()), Channels: []jetcast.Channel{jetcast.Private(channel)}, Data: json.RawMessage(data)})
	return errors.Join(revocationErr, err)
}

// reauthorize 按数据库中的账号归属强制关闭连接，客户端复核会话并获取当前成员授权。
func (s *Service) reauthorize(ctx context.Context, notification realtime.Notification) error {
	accountID := notification.AudienceID
	if notification.AudienceKind != realtime.AudienceAccount {
		if err := s.db.NewSelect().Model((*servermodels.User)(nil)).Column("account_id").Where("id = ? AND workspace_id = ?", notification.AudienceID, notification.WorkspaceID).Scan(ctx, &accountID); err != nil {
			return err
		}
	}
	target := jetcast.ByUser("a_" + accountID)
	if notification.TokenSessionID != "" {
		target = jetcast.BySession(target.User, notification.TokenSessionID)
	}
	result, err := s.Server.Disconnect(ctx, target)
	if err == nil && (!result.Enforced || result.Failed != 0) {
		err = errors.New("member revocation was not enforced")
	}
	return errors.Join(err, s.revokeInvalidComputers(ctx, accountID))
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
	channel := []jetcast.Channel{jetcast.Private(realtime.RunChannel(workspaceID, event.RunID))}
	// 结束批次没有增量也无需重读快照时只发送结束帧。
	if event.Delta != nil || event.Invalidated || !event.Ended {
		_, err = s.Server.Broadcast(ctx, jetcast.Event{Name: string(frame.FrameType()), Channels: channel, Data: json.RawMessage(data)})
	}
	if !event.Ended {
		return err
	}
	// 结束帧独立发送，增量帧失败时仍然通知客户端结束。
	frame = protocol.RunStreamEnded{RunID: event.RunID}
	data, encodeErr := protocol.Encode(frame)
	if encodeErr != nil {
		return errors.Join(err, encodeErr)
	}
	_, endErr := s.Server.Broadcast(ctx, jetcast.Event{Name: string(frame.FrameType()), Channels: channel, Data: json.RawMessage(data)})
	return errors.Join(err, endErr)
}
