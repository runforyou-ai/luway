//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestMessageSequenceCommitOrder 验证所有会话类型在提交前阻塞后续分配，回滚撤销事务内已写入的摘要并允许编号重用。
func TestMessageSequenceCommitOrder(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	for _, kind := range []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeAgent, domain.ConversationTypeGroup, domain.ConversationTypeChannel} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback=%t", kind, rollback), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cv := &servermodels.Conversation{ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, Type: string(kind), Status: string(domain.ConversationStatusActive)}
				_, err := f.db.NewInsert().Model(cv).Column("id", "workspace_id", "type", "status").Exec(ctx)
				require.NoError(t, err)
				errRollback := errors.New("rollback")
				appended := make(chan *servermodels.Message, 1)
				finish := make(chan struct{}, 1)
				held := make(chan error, 1)
				// 首个事务追加消息后持有会话锁，直到收到提交或回滚信号。
				go func() {
					held <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
						if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
							return err
						}
						message, _, err := chatstate.AppendSystemEvent(ctx, tx, cv, &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "system", Body: "先取锁", OriginatedAt: time.Now().UTC()})
						if err != nil {
							return err
						}
						// 回滚用例在事务内核验摘要已随消息写入。
						if rollback {
							var lastID string
							if err := tx.NewSelect().Model(cv).Column("last_message_id").WherePK().Scan(ctx, &lastID); err != nil {
								return err
							}
							if lastID != message.ID {
								return fmt.Errorf("summary not written before rollback: %s", lastID)
							}
						}
						appended <- message
						select {
						case <-finish:
						case <-ctx.Done():
							return ctx.Err()
						}
						if rollback {
							return errRollback
						}
						return nil
					})
				}()
				var first *servermodels.Message
				select {
				case first = <-appended:
				case err := <-held:
					t.Fatalf("first transaction err=%v", err)
				}
				require.Equal(t, int64(1), first.MessageSeq)
				done := make(chan error, 1)
				var second *servermodels.Message
				go func() {
					done <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, next bun.Tx) error {
						locked := &servermodels.Conversation{ID: cv.ID}
						if err := next.NewSelect().Model(locked).WherePK().For("UPDATE").Scan(ctx); err != nil {
							return err
						}
						var err error
						second, _, err = chatstate.AppendSystemEvent(ctx, next, locked, &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "system", Body: "后取锁但来源更早", OriginatedAt: first.OriginatedAt.Add(-time.Hour)})
						return err
					})
				}()
				waitConversationLock(t, ctx, f.db, cv.ID)
				count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", cv.ID).Count(ctx)
				require.NoError(t, err)
				require.Equal(t, int64(0), count, "uncommitted count")
				want := int64(2)
				if rollback {
					want = 1
				}
				finish <- struct{}{}
				if err := waitChatResult(t, ctx, held); rollback {
					require.ErrorIs(t, err, errRollback, "first transaction")
				} else {
					require.NoError(t, err, "first transaction")
				}
				require.NoError(t, waitChatResult(t, ctx, done))
				require.Equal(t, want, second.MessageSeq, "sequence")
				require.NoError(t, f.db.NewSelect().Model(cv).WherePK().Scan(ctx))
				require.Equal(t, want, cv.LastMessageSeq)
				require.Equal(t, &second.ID, cv.LastMessageID)
				count, err = f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", cv.ID).Count(ctx)
				require.NoError(t, err)
				require.Equal(t, want, count, "visible count")
			})
		}
	}
}

// TestMessageSequenceHTTPContract 验证真实数据库经过应用服务与 HTTP 后仍以字符串传输大整数序号。
func TestMessageSequenceHTTPContract(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_message_seq = 9007199254740992").Where("id = ?", f.conversationID).Exec(ctx)
	require.NoError(t, err)
	sent, err := f.visitorMessage(ctx, "HTTP 大整数")
	require.NoError(t, err)
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.owner.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := api.NewService(backend)
	for _, route := range []string{"messages", "read"} {
		t.Run(route, func(t *testing.T) {
			method := http.MethodGet
			var body bytes.Buffer
			if route == "read" {
				method = http.MethodPost
				require.NoError(t, json.NewEncoder(&body).Encode(appservice.MarkConversationReadInput{LastReadMessageID: sent.Message.ID}))
			}
			request := httptest.NewRequest(method, "/conversations/"+f.conversationID+"/"+route, &body)
			request.Header.Set("Authorization", "Bearer "+login.Token)
			request.Header.Set(appservice.WorkspaceHeader, f.owner.Workspace.ID)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			service.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, "body=%s", response.Body.String())
			key := "messageSeq"
			if route == "read" {
				key = "readSeq"
			}
			require.Contains(t, response.Body.String(), `"`+key+`":"9007199254740993"`, "lossy response")
		})
	}
	visitor := direct.NewWebsiteVisitorBackend(f.db, nil, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", page.Messages[len(page.Messages)-1].MessageSeq)
	next, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{After: *page.After})
	require.NoError(t, err)
	require.Empty(t, next.Messages, "cursor")
}
