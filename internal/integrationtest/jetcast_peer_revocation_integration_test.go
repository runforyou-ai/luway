//go:build server

package integrationtest

import (
	"context"
	"github.com/nats-io/nats.go"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
	"uuid"
)

// TestJetcastVisitorRevocationRetry 验证渠道停用后的持久补偿精确关闭原 NATS 连接。
func TestJetcastVisitorRevocationRetry(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	var admin *failingKickAdmin
	h := startVisitorRealtime(t, f, func(connection *broker.Connection) {
		admin = &failingKickAdmin{ConnectionAdmin: connection.Admin}
		connection.Admin = admin
		tasks, err := servertask.New(t.Context(), connection.JS, servertask.Options{Namespace: "visitor-revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
		require.NoError(t, err)
		require.NoError(t, connection.RegisterRevocations(tasks, f.db))
	})
	config := visitorConfig(t, h, f.channelID, "0123456789abcdef0123456789abcdef", "")
	raw := rawPeer(t, h.url, config)
	serverID := raw.ConnectedServerId()
	cid, err := raw.GetClientID()
	require.NoError(t, err)
	_, err = newTestChannelStatusAction(f.db).Execute(t.Context(), f.owner, f.channelID, false)
	require.NoError(t, err)
	require.False(t, raw.IsClosed())
	admin.mu.Lock()
	targets := len(admin.targets)
	admin.mu.Unlock()
	require.Equal(t, 1, targets)
	tasks, err := servertask.New(t.Context(), h.broker.JS, servertask.Options{Namespace: "visitor-revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
	require.NoError(t, err)
	retry := &broker.Connection{Admin: admin}
	require.NoError(t, retry.RegisterRevocations(tasks, f.db))
	require.NoError(t, tasks.Start(t.Context()))
	t.Cleanup(tasks.Stop)
	require.Eventually(t, raw.IsClosed, 45*time.Second, 20*time.Millisecond)
	admin.mu.Lock()
	defer admin.mu.Unlock()
	require.Len(t, admin.targets, 7)
	for index, target := range admin.targets {
		require.Equal(t, serverID, target)
		require.Equal(t, cid, admin.cids[index])
	}
}

// workspacePeerConnections 为工作区暂停测试建立忽略控制消息的访客与电脑连接。
func workspacePeerConnections(t *testing.T, h *realtimeGatewayHarness, workspaceID, token string) []*nats.Conn {
	t.Helper()
	ctx := context.Background()
	identity := servertest.ResolveMemberSession(t, h.db, workspaceID, token).Identity
	computer, err := computeraction.NewCreateWorkspaceComputerAction(h.db).Execute(ctx, identity, "暂停测试电脑")
	require.NoError(t, err)
	config, err := h.backend.GetComputerRealtimeConnection(ctx, appservice.RequestMeta{Token: computer.Credential, ExecutorVersion: domain.ExecutorVersion})
	require.NoError(t, err)
	peers := []*nats.Conn{rawPeer(t, h.url, config)}
	channel, err := channelaction.NewCreateMessageChannelAction(h.db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "暂停测试访客", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	visitor := direct.NewWebsiteVisitorBackend(h.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer, nil, servertest.TestDeployment(t, h.db).S3, servertest.DisabledMail{}, nil)
	external := "web-session:0123456789abcdef0123456789abcdef"
	_, err = visitor.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, channel.ID, external, appservice.WebsiteVisitorTextMessageInput{ClientMessageID: uuid.NewV7().String(), Body: "工作区暂停测试"})
	require.NoError(t, err)
	config, err = visitor.GetVisitorRealtimeConnection(ctx, appservice.WebsiteVisitorMeta{}, channel.ID, external)
	require.NoError(t, err)
	return append(peers, rawPeer(t, h.url, config))
}

// TestJetcastComputerOwnerRevocation 验证个人电脑随成员停用失权，同账号另一工作区电脑继续工作。
func TestJetcastComputerOwnerRevocation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	ctx := t.Context()
	other := servertest.AddAccountWorkspace(t, f.db, f.memberToken, "保留的电脑工作区")
	first, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, f.member, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "停用的成员电脑"})
	require.NoError(t, err)
	second, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, other, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "有效的成员电脑"})
	require.NoError(t, err)
	meta := appservice.RequestMeta{Token: first.Credential, ExecutorVersion: domain.ExecutorVersion}
	config, err := h.backend.GetComputerRealtimeConnection(ctx, meta)
	require.NoError(t, err)
	old := rawPeer(t, h.url, config)
	current := connectPeer(t, f.db, h.url, config)
	secondMeta := appservice.RequestMeta{Token: second.Credential, ExecutorVersion: domain.ExecutorVersion}
	validConfig, err := h.backend.GetComputerRealtimeConnection(ctx, secondMeta)
	require.NoError(t, err)
	valid := connectPeer(t, f.db, h.url, validConfig)
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	require.Eventually(t, old.IsClosed, 5*time.Second, 10*time.Millisecond)
	current.expectEnded()
	require.Error(t, h.backend.HeartbeatComputer(ctx, meta))
	require.NoError(t, h.backend.HeartbeatComputer(ctx, secondMeta))
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(other.Workspace.ID, second.Record.ID)))
	valid.expect(protocol.ComputerWork{})
}
