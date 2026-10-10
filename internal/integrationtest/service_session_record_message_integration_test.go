//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestServiceSessionRecordMessagePaths 验证消息推进周期的两条路径写入结果一致：调用方传入已锁定周期时走纯函数流转，未传入时走单条 UPDATE。
func TestServiceSessionRecordMessagePaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLifecycleSessionFixture(t)
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	ago := func(minutes int) *time.Time {
		return new(base.Add(-time.Duration(minutes) * time.Minute))
	}
	open, closed := string(domain.ServiceSessionStatusOpen), string(domain.ServiceSessionStatusClosed)
	cases := []struct {
		name          string
		status        string
		awaiting      *time.Time
		reminded      *time.Time
		resolution    *time.Time
		fromRequester bool
	}{
		{"客户来信开始等待", open, nil, ago(3), ago(2), true},
		{"客户追问延续等待与提醒", open, ago(10), ago(3), ago(2), true},
		{"处理方回复结束等待", open, ago(10), ago(3), ago(2), false},
		{"处理方在无等待时回复", open, nil, nil, ago(2), false},
		{"已关闭周期不变", closed, ago(10), ago(3), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messageID := uuid.NewV7().String()
			results := make([]servermodels.ServiceSession, 0, 2)
			for _, passLocked := range []bool{true, false} {
				input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.queueCh, ExternalID: lifecycleSessionVisitor()}
				conversationID := f.receive(t, &input, "两条路径").Conversation.ID
				session := f.current(t, conversationID)
				_, err := f.db.NewUpdate().Model(&session).
					Set("status = ?", tc.status).Set("awaiting_reply_since = ?", tc.awaiting).
					Set("reminded_at = ?", tc.reminded).Set("resolution_requested_at = ?", tc.resolution).
					WherePK().Exec(ctx)
				require.NoError(t, err)
				preparedUpdatedAt := loadSession(t, f.db, session.ID).UpdatedAt
				// 发起人消息取访客参与者，处理方消息取不属于发起人的参与者编号。
				senderID := uuid.NewV7().String()
				if tc.fromRequester {
					require.NoError(t, f.db.NewSelect().TableExpr("conversation_participants AS cp").Column("cp.id").
						Join("JOIN service_conversations AS svc ON svc.workspace_id = cp.workspace_id AND svc.conversation_id = cp.conversation_id AND svc.requester_subject_id = cp.subject_id").
						Where("cp.conversation_id = ?", conversationID).Scan(ctx, &senderID))
				}
				message := &servermodels.Message{
					ID: messageID, WorkspaceID: session.WorkspaceID, ConversationID: conversationID,
					ServiceSessionID: &session.ID, SenderParticipantID: &senderID, OriginatedAt: base,
				}
				var memory *servermodels.ServiceSession
				require.NoError(t, f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					var locked *servermodels.ServiceSession
					if passLocked {
						locked = &servermodels.ServiceSession{}
						if err := tx.NewSelect().Model(locked).Where("ss.id = ?", session.ID).For("UPDATE").Scan(ctx); err != nil {
							return err
						}
						memory = locked
					}
					return servicestate.RecordMessage(ctx, tx, locked, message)
				}))
				saved := loadSession(t, f.db, session.ID)
				if memory != nil {
					if diff := cmp.Diff(saved, *memory); diff != "" {
						t.Fatalf("纯函数路径内存值与落库值不符 (-db +memory):\n%s", diff)
					}
				}
				// 开放周期推进 updated_at，已关闭周期保持预置后的值。
				if tc.status == open {
					require.True(t, saved.UpdatedAt.After(preparedUpdatedAt), "开放周期 updated_at 未推进: %s", saved.UpdatedAt)
				} else {
					require.True(t, saved.UpdatedAt.Equal(preparedUpdatedAt), "已关闭周期 updated_at 被改写: %s", saved.UpdatedAt)
				}
				// 开放周期的最后消息推进为本条，已关闭周期保持原值。
				if tc.status == open {
					require.Equal(t, messageID, saved.LastMessageID)
					require.True(t, base.Equal(saved.LastMessageAt))
				} else {
					require.Equal(t, session.LastMessageID, saved.LastMessageID)
					require.True(t, session.LastMessageAt.Equal(saved.LastMessageAt))
				}
				results = append(results, saved)
			}
			if diff := cmp.Diff(results[0], results[1], cmpopts.IgnoreFields(servermodels.ServiceSession{},
				"ID", "CreatedAt", "UpdatedAt", "ConversationID", "ServiceConversationID", "OpeningMessageID", "LastMessageID", "LastMessageAt", "StatusChangedAt",
				"AssignedAt", "AssigneeAssignedAt", "QueuedAt", "HumanRequestedAt", "VisitorContext")); diff != "" {
				t.Fatalf("两条路径写入不一致 (-locked +sql):\n%s", diff)
			}
		})
	}
}
