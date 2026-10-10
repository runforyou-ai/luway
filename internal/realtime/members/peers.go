//go:build server

package members

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/runforyou-ai/jetcast"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"sync"
	"time"
)

// revokeInvalidComputers 关闭账号所属且已失去业务资格的个人电脑连接。
func (s *Service) revokeInvalidComputers(ctx context.Context, accountID string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var ids []string
	err := s.db.NewRaw(`SELECT c.id::text FROM computers c JOIN users u ON u.id = c.owner_user_id
 JOIN accounts a ON a.id = u.account_id JOIN workspaces w ON w.id = c.workspace_id
 WHERE a.id = ? AND (u.status <> ? OR a.status <> ? OR w.lifecycle_status <> ? OR c.revoked_at IS NOT NULL)`,
		accountID, domain.IdentityStatusActive, domain.AccountStatusActive, domain.WorkspaceLifecycleActive).Scan(ctx, &ids)
	if err != nil {
		return err
	}
	return s.revokePeers(ctx, "c_", ids)
}

// peerRevocationConcurrency 是按主体撤销访客或电脑连接时的并发数。
const peerRevocationConcurrency = 16

// revokePeers 以每个主体的独立时限、有限并发登记并强制关闭其原连接。
func (s *Service) revokePeers(ctx context.Context, kind string, ids []string) error {
	var (
		mu     sync.Mutex
		result error
		group  sync.WaitGroup
	)
	slots := make(chan struct{}, peerRevocationConcurrency)
	for _, id := range ids {
		slots <- struct{}{}
		group.Go(func() {
			defer func() { <-slots }()
			attempt, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			revoked, err := s.Server.Disconnect(attempt, jetcast.ByUser(kind+id))
			cancel()
			if err == nil && (!revoked.Enforced || revoked.Failed != 0) {
				err = errors.New("peer revocation was not enforced")
			}
			mu.Lock()
			result = errors.Join(result, err)
			mu.Unlock()
		})
	}
	group.Wait()
	return result
}

// publishPeer 向精确访客或电脑频道发布通知，并按业务主体强制撤销连接。
func (s *Service) publishPeer(ctx context.Context, n realtime.Notification) error {
	ids := []string{n.AudienceID}
	kind := "v_"
	if n.AudienceKind == realtime.AudienceComputer {
		kind = "c_"
	}
	if n.AudienceKind == realtime.AudienceWebsiteChannel || n.AudienceKind == realtime.AudienceCustomerIdentity {
		q := s.db.NewSelect().TableExpr("channel_identities ci").ColumnExpr("ci.id::text").Where("ci.workspace_id = ?", n.WorkspaceID).
			Where("ci.external_id LIKE ? OR ci.external_id LIKE ?", "web-session:%", "web-user:%")
		if n.AudienceKind == realtime.AudienceWebsiteChannel {
			q = q.Where("ci.channel_id = ?", n.AudienceID)
		}
		if n.AudienceKind == realtime.AudienceCustomerIdentity {
			q = q.Where("ci.external_id LIKE ?", "web-user:%")
		}
		if err := q.Scan(ctx, &ids); err != nil {
			return err
		}
	}
	if realtime.IsAuthorizationChange(n.Kind) {
		return s.revokePeers(ctx, kind, ids)
	}
	var result error
	for _, id := range ids {
		var frame protocol.Frame
		switch n.Kind {
		case realtime.KindConversationChanged:
			frame = protocol.ConversationChanged{ConversationID: n.ConversationID, ConversationType: n.ConversationType, Version: n.Version, Changes: n.Changes}
		case realtime.KindVisitorTyping:
			frame = protocol.VisitorTyping{ConversationID: n.ConversationID, Active: n.Active}
		case realtime.KindReceptionChanged:
			frame = protocol.ReceptionChanged{}
		case realtime.KindComputerWork:
			frame = protocol.ComputerWork{}
		}
		if frame == nil {
			continue
		}
		channel := realtime.VisitorChannel(n.WorkspaceID, id)
		if n.AudienceKind == realtime.AudienceWebsiteVisitors {
			channel = "w." + n.WorkspaceID + ".visitors"
		}
		if kind == "c_" {
			channel = realtime.ComputerChannel(n.WorkspaceID, id)
		}
		if n.Kind == realtime.KindVisitorTyping {
			channel += ".typing"
		}
		data, err := protocol.Encode(frame)
		if err == nil {
			_, err = s.Server.Broadcast(ctx, jetcast.Event{Name: string(frame.FrameType()), Channels: []jetcast.Channel{jetcast.Private(channel)}, Data: json.RawMessage(data)})
		}
		result = errors.Join(result, err)
	}
	return result
}
