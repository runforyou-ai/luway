//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/jetcast/client"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
)

// TestJetcastNamespace 验证合法命名空间映射互不冲突，并经真实业务认证订阅成员通知。
func TestJetcastNamespace(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	token := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	prefixes := map[string]bool{}
	for _, namespace := range []string{"app", "app-prod", "app_hprod"} {
		t.Run(namespace, func(t *testing.T) {
			config := serverconfig.NATSConfig{Namespace: namespace}
			prefix := config.RealtimePrefix()
			require.False(t, prefixes[prefix], "不同命名空间须生成不同前缀")
			prefixes[prefix] = true
			connection := servertest.StartNamespaceBroker(t, namespace)
			backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db), RealtimePrefix: prefix}, nil, nil, nil, testEnqueuer, nil, nil, nil)
			service, err := members.New(connection, backend, f.db, prefix, 1)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			t.Cleanup(cancel)
			require.NoError(t, service.Start(ctx))
			t.Cleanup(func() { require.NoError(t, service.Stop()) })
			info, err := backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token})
			require.NoError(t, err)
			require.Equal(t, prefix, info.Prefix)
			require.Len(t, info.Members, 1)
			echo, err := client.Connect(ctx, client.Options{
				Servers: []string{"ws" + strings.TrimPrefix(connection.WSURL, "http")}, Prefix: info.Prefix,
				GetToken: func(context.Context) (string, error) { return token, nil },
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, echo.Close()) })
			events := make(chan client.Event, 1)
			sub := echo.Private(info.Members[0].Channel)
			sub.ListenAll(func(event client.Event) {
				select {
				case events <- event:
				case <-ctx.Done():
				}
			})
			require.NoError(t, sub.Ready(ctx))
			notice := realtime.UserNotificationFor(f.owner.Workspace.ID, f.member.User.ID, uuid.NewV7().String(), f.groupID, domain.NotificationViewGroup, "命名空间通知", namespace)
			require.NoError(t, service.Publish(ctx, notice))
			expected, err := protocol.Encode(members.NotificationFrame(notice))
			require.NoError(t, err)
			select {
			case event := <-events:
				require.JSONEq(t, string(expected), string(event.Data))
			case <-ctx.Done():
				t.Fatal("未收到成员通知")
			}
		})
	}
}
