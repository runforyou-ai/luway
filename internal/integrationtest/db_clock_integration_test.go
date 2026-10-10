//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestComputerOnlineUsesDatabaseClock 验证电脑列表的在线状态按数据库时刻与最近在线时间判断。
func TestComputerOnlineUsesDatabaseClock(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, f.owner, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "时钟电脑"})
	require.NoError(t, err)
	require.False(t, registered.Record.Online, "新凭据尚未连接")

	online := func(lastSeen string) bool {
		t.Helper()
		_, err := f.db.NewUpdate().Model((*servermodels.Computer)(nil)).Set("last_seen_at = "+lastSeen).Where("id = ?", registered.Record.ID).Exec(ctx)
		require.NoError(t, err)
		records := listComputers(t, f.db, f.owner)
		require.Len(t, records, 1)
		return records[0].Online
	}
	require.True(t, online("now()"), "刚记录在线")
	require.False(t, online("now() - make_interval(secs => 61)"), "超过在线时限")
}
