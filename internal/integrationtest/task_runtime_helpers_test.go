//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/servertest"

	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/domain"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testEnqueuer 是全部测试共用的任务登记器，断言时按工作区筛选任务。
var testEnqueuer = servertest.NewTasks()

// disableAutoAssignment 把企业内全部成员的最大接待量设为 0，使只验证路由去向的测试不触发自动分配。
func disableAutoAssignment(t *testing.T, db *bun.DB, workspaceID string) {
	t.Helper()
	_, err := db.NewUpdate().Table("users").Set("max_service_sessions = 0").Where("workspace_id = ?", workspaceID).Exec(context.Background())
	require.NoError(t, err)
}

// newTestChannelStatusAction 创建渠道启停操作，Telegram 渠道使用不调用外部接口的机器人接口替身。
func newTestChannelStatusAction(db *bun.DB) *channelaction.UpdateMessageChannelStatusAction {
	return channelaction.NewUpdateMessageChannelStatusAction(db, testEnqueuer, map[domain.ChannelType]channelaction.StatusUpdater{
		domain.ChannelTypeTelegram: telegramaction.NewUpdateChannelStatusAction(db, connectiontest.NewRunner(time.Second), &telegramBotAPIFake{}, testEnqueuer),
	})
}
