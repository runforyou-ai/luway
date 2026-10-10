//go:build server

package integrationtest

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestInboxSearchAcrossMessagePartitions 验证跨会话检索分段查找较早分区中的消息并按消息编号倒序返回，工作区创建时间晚于当前月份时返回空结果。
func TestInboxSearchAcrossMessagePartitions(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	now := time.Now()
	_, err := f.db.NewUpdate().Table("workspaces").Set("created_at = ?", now.AddDate(0, -3, 0)).Where("id = ?", f.owner.Workspace.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, serverstorage.EnsureMessagePartitions(ctx, f.db, now.AddDate(0, -3, 0)))

	// 编号时间取两个月前，写入该月份的分区。
	id := uuid.NewV7()
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(now.AddDate(0, -2, 0).UnixMilli()))
	copy(id[:6], stamp[2:])
	old := &servermodels.Message{ID: id.String(), WorkspaceID: f.owner.Workspace.ID, ConversationID: f.groupID, Type: "text", Body: "潮汐观测记录", OriginatedAt: now.AddDate(0, -2, 0)}
	err = realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		conversation := &servermodels.Conversation{ID: f.groupID, WorkspaceID: f.owner.Workspace.ID}
		if err := tx.NewSelect().Model(conversation).WherePK().For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		_, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, conversation, old)
		return err
	})
	require.NoError(t, err)
	var partition string
	require.NoError(t, f.db.NewRaw("SELECT tableoid::regclass::text FROM messages WHERE id = ?", old.ID).Scan(ctx, &partition))
	require.Equal(t, "messages_"+serverstorage.MessageMonthStart(now.AddDate(0, -2, 0)).Format("200601"), partition, "较早消息写入分区")
	latest := f.send(t, f.owner, "潮汐观测结论", false)

	query := inboxaction.NewLoadInboxQuery(f.db)
	search := func(text string) []string {
		t.Helper()
		result, err := query.Search(ctx, f.owner, inboxaction.SearchInput{Text: text, Range: inboxaction.SearchRangeReadable})
		require.NoError(t, err)
		return arr.Map(result.Messages, func(message inboxaction.SearchMessage) string { return message.ID })
	}
	require.Equal(t, []string{latest.ID, old.ID}, search("潮汐观测"), "跨分区检索结果")
	require.Equal(t, []string{old.ID}, search("观测记录"), "较早分区检索结果")
	_, err = f.db.NewUpdate().Table("workspaces").Set("created_at = ?", now.AddDate(0, 2, 0)).Where("id = ?", f.owner.Workspace.ID).Exec(ctx)
	require.NoError(t, err)
	require.Empty(t, search("潮汐观测"), "工作区创建时间晚于当前月份时的检索结果")
}
