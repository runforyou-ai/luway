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
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
				cv := &servermodels.Conversation{ID: uuid.NewV7().String(), OrganizationID: f.owner.Organization.ID, Type: string(kind), Status: string(domain.ConversationStatusActive)}
				if _, err := f.db.NewInsert().Model(cv).Column("id", "organization_id", "type", "status").Exec(ctx); err != nil {
					t.Fatal(err)
				}
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
						message, _, err := chatstate.AppendMessage(ctx, tx, cv, &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: cv.OrganizationID, ConversationID: cv.ID, Type: "system", Body: "先取锁", OriginatedAt: time.Now().UTC()})
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
				if first.MessageSeq != 1 {
					t.Fatalf("first=%+v", first)
				}
				done := make(chan error, 1)
				var second *servermodels.Message
				go func() {
					done <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, next bun.Tx) error {
						locked := &servermodels.Conversation{ID: cv.ID}
						if err := next.NewSelect().Model(locked).WherePK().For("UPDATE").Scan(ctx); err != nil {
							return err
						}
						var err error
						second, _, err = chatstate.AppendMessage(ctx, next, locked, &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: cv.OrganizationID, ConversationID: cv.ID, Type: "system", Body: "后取锁但来源更早", OriginatedAt: first.OriginatedAt.Add(-time.Hour)})
						return err
					})
				}()
				waitConversationLock(t, ctx, f.db, cv.ID)
				if count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", cv.ID).Count(ctx); err != nil || count != 0 {
					t.Fatalf("uncommitted count=%d err=%v", count, err)
				}
				want := int64(2)
				if rollback {
					want = 1
				}
				finish <- struct{}{}
				if err := waitChatResult(t, ctx, held); rollback && !errors.Is(err, errRollback) || !rollback && err != nil {
					t.Fatalf("first transaction err=%v", err)
				}
				if err := waitChatResult(t, ctx, done); err != nil {
					t.Fatal(err)
				}
				if second.MessageSeq != want {
					t.Fatalf("sequence=%d want=%d", second.MessageSeq, want)
				}
				if err := f.db.NewSelect().Model(cv).WherePK().Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if cv.LastMessageSeq != want || cv.LastMessageID == nil || *cv.LastMessageID != second.ID {
					t.Fatalf("summary=%+v", cv)
				}
				if count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", cv.ID).Count(ctx); err != nil || int64(count) != want {
					t.Fatalf("visible count=%d err=%v", count, err)
				}
			})
		}
	}
}

// TestMessageSequenceLargeReadAndWindows 验证大整数序号的双向分页、引用定位与访客读取。
func TestMessageSequenceLargeReadAndWindows(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	const base int64 = 9007199254740991
	if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_message_seq = ?", base).Where("id = ?", f.conversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var sent []customerchataction.Message
	for range 3 {
		result, err := f.visitorMessage(ctx, "大整数消息")
		if err != nil {
			t.Fatal(err)
		}
		sent = append(sent, result.Message)
	}
	for i, message := range sent {
		if message.MessageSeq != base+int64(i)+1 {
			t.Fatalf("message=%+v", message)
		}
	}
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	point := &conversationaction.MessageCursorPoint{ID: sent[1].ID, MessageSeq: sent[1].MessageSeq}
	after, err := query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, After: point})
	if err != nil || len(after.Messages) != 1 || after.Messages[0].ID != sent[2].ID {
		t.Fatalf("after=%+v err=%v", after, err)
	}
	before, err := query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, Before: point})
	if err != nil || len(before.Messages) != 2 || before.Messages[1].ID != sent[0].ID {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	around, err := query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, AroundMessageID: sent[1].ID})
	if err != nil || len(around.Messages) != 4 || around.Messages[2].MessageSeq != point.MessageSeq {
		t.Fatalf("around=%+v err=%v", around, err)
	}
	visitor, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: f.conversationID, After: point})
	if err != nil || len(visitor.Messages) != 1 || visitor.Messages[0].MessageSeq != sent[2].MessageSeq {
		t.Fatalf("visitor=%+v err=%v", visitor, err)
	}
}

// TestMessageSequenceHTTPContract 验证真实数据库经过应用服务与 HTTP 后仍以字符串传输大整数序号。
func TestMessageSequenceHTTPContract(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_message_seq = 9007199254740992").Where("id = ?", f.conversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sent, err := f.visitorMessage(ctx, "HTTP 大整数")
	if err != nil {
		t.Fatal(err)
	}
	login := loginMember(t, f.db, f.owner.Organization.ID, f.owner.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	service := api.NewService(appservice.New(backend))
	for _, route := range []string{"messages", "read"} {
		t.Run(route, func(t *testing.T) {
			method := http.MethodGet
			var body bytes.Buffer
			if route == "read" {
				method = http.MethodPost
				if err := json.NewEncoder(&body).Encode(appservice.MarkConversationReadInput{LastReadMessageID: sent.Message.ID}); err != nil {
					t.Fatal(err)
				}
			}
			request := httptest.NewRequest(method, "/conversations/"+f.conversationID+"/"+route, &body)
			request.Header.Set("Authorization", "Bearer "+login.Token)
			request.Header.Set(appservice.WorkspaceHeader, f.owner.Organization.ID)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			service.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			key := "messageSeq"
			if route == "read" {
				key = "readSeq"
			}
			if !strings.Contains(response.Body.String(), `"`+key+`":"9007199254740993"`) {
				t.Fatalf("lossy response: %s", response.Body.String())
			}
		})
	}
	visitor := direct.NewWebsiteVisitorBackend(f.db, nil, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	if err != nil || page.Messages[len(page.Messages)-1].MessageSeq != "9007199254740993" {
		t.Fatalf("visitor=%+v err=%v", page, err)
	}
	next, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{After: *page.After})
	if err != nil || len(next.Messages) != 0 {
		t.Fatalf("cursor=%+v err=%v", next, err)
	}
}
