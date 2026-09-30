//go:build server

package integrationtest

import (
	"context"
	"encoding/binary"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/realtime"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestInboxSearchAcrossMessagePartitions 验证跨会话检索分段查找较早分区中的消息并按消息编号倒序返回，工作区创建时间晚于当前月份时返回空结果。
func TestInboxSearchAcrossMessagePartitions(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := f.db.NewUpdate().Table("organizations").Set("created_at = ?", now.AddDate(0, -3, 0)).Where("id = ?", f.owner.Organization.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := serverstorage.EnsureMessagePartitions(ctx, f.db, now.AddDate(0, -3, 0)); err != nil {
		t.Fatal(err)
	}

	// 编号时间取两个月前，写入该月份的分区。
	id := uuid.NewV7()
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(now.AddDate(0, -2, 0).UnixMilli()))
	copy(id[:6], stamp[2:])
	old := &servermodels.Message{ID: id.String(), OrganizationID: f.owner.Organization.ID, ConversationID: f.groupID, Type: "text", Body: "潮汐观测记录", OriginatedAt: now.AddDate(0, -2, 0)}
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		conversation := &servermodels.Conversation{ID: f.groupID, OrganizationID: f.owner.Organization.ID}
		if err := tx.NewSelect().Model(conversation).WherePK().For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		_, _, err := chatstate.AppendMessage(ctx, tx, conversation, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var partition string
	if err := f.db.NewRaw("SELECT tableoid::regclass::text FROM messages WHERE id = ?", old.ID).Scan(ctx, &partition); err != nil {
		t.Fatal(err)
	}
	if expected := "messages_" + serverstorage.MessageMonthStart(now.AddDate(0, -2, 0)).Format("200601"); partition != expected {
		t.Fatalf("较早消息写入分区 %s，期望 %s", partition, expected)
	}
	latest := f.send(t, f.owner, "潮汐观测结论", false)

	query := inboxaction.NewLoadInboxQuery(f.db)
	search := func(text string) []string {
		t.Helper()
		result, err := query.Search(ctx, f.owner, inboxaction.SearchInput{Text: text, Range: inboxaction.SearchRangeReadable})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(result.Messages))
		for _, message := range result.Messages {
			ids = append(ids, message.ID)
		}
		return ids
	}
	if ids := search("潮汐观测"); !slices.Equal(ids, []string{latest.ID, old.ID}) {
		t.Fatalf("跨分区检索结果 = %v，期望 [%s %s]", ids, latest.ID, old.ID)
	}
	if ids := search("观测记录"); !slices.Equal(ids, []string{old.ID}) {
		t.Fatalf("较早分区检索结果 = %v，期望 [%s]", ids, old.ID)
	}
	if _, err := f.db.NewUpdate().Table("organizations").Set("created_at = ?", now.AddDate(0, 2, 0)).Where("id = ?", f.owner.Organization.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if ids := search("潮汐观测"); len(ids) != 0 {
		t.Fatalf("工作区创建时间晚于当前月份时的检索结果 = %v", ids)
	}
}
