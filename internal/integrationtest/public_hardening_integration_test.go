//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/ratelimit"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestRateLimitConsumesBurst 验证限速键在突发额度内放行，用完后按恢复间隔给出重试时间，前一检查项超限时后续检查项不消耗额度。
func TestRateLimitConsumesBurst(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	limiter := ratelimit.NewLimiter(db)
	subject := uuid.NewV7().String()
	for range 10 {
		require.NoError(t, limiter.Allow(ctx, ratelimit.RegisterByIP.For(subject)))
	}
	var limited *ratelimit.LimitedError
	other := uuid.NewV7().String()
	err = limiter.Allow(ctx, ratelimit.RegisterByIP.For(subject), ratelimit.LoginByEmail.For(other))
	require.ErrorAs(t, err, &limited)
	require.Greater(t, limited.RetryAfter, time.Duration(0))
	require.LessOrEqual(t, limited.RetryAfter, time.Minute)
	// 超限的检查项之后的额度未被消耗。
	for range 10 {
		require.NoError(t, limiter.Allow(ctx, ratelimit.LoginByEmail.For(other)))
	}
	require.ErrorAs(t, limiter.Allow(ctx, ratelimit.LoginByEmail.For(other)), &limited)
	// 对象为空的检查项不限速。
	require.NoError(t, limiter.Allow(ctx, ratelimit.LoginByIP.For("")))
	// 额度未恢复的记录不会被清理。
	require.NoError(t, ratelimit.Prune(ctx, db))
	count, err := db.NewSelect().TableExpr("rate_limits").Where("key = ?", "register_ip:"+subject).Count(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// TestRateLimitConcurrentRetryAfter 验证并发请求按顺序消耗额度，被拒绝的请求都按额度用完后的状态给出重试时间。
func TestRateLimitConcurrentRetryAfter(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	limiter := ratelimit.NewLimiter(db)
	subject := uuid.NewV7().String()
	results := make(chan error, 20)
	for range 20 {
		go func() { results <- limiter.Allow(ctx, ratelimit.RegisterByIP.For(subject)) }()
	}
	allowed := 0
	for range 20 {
		err := <-results
		if err == nil {
			allowed++
			continue
		}
		var limited *ratelimit.LimitedError
		require.ErrorAs(t, err, &limited)
		require.GreaterOrEqual(t, limited.RetryAfter, 50*time.Second, "retry after reflects consumed quota")
	}
	require.Equal(t, 10, allowed)
}

// TestWebsiteVisitorUnrepliedConversationLimit 验证开启多会话的访客已有 3 个进行中且未收到回复的会话时不能再新建会话，已有会话照常发送，会话收到回复或结束后可以再新建。
func TestWebsiteVisitorUnrepliedConversationLimit(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := func(conversationID *string) (customerchataction.ReceiveWebsiteCustomerMessageResult, error) {
		return f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ConversationID: conversationID,
			ClientMessageID: uuid.NewV7().String(), Body: "又一个问题",
		})
	}
	// 夹具已有一个未回复的会话，再新建两个达到上限。
	for range channelinboundaction.MaxUnrepliedConversations - 1 {
		result, err := send(nil)
		require.NoError(t, err)
		require.True(t, result.CreatedConversation)
	}
	var conflict *conversationaction.ConflictError
	_, err := send(nil)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, channelinboundaction.ConflictReasonUnrepliedConversationsExceeded, conflict.Reason)
	_, err = send(&f.conversationID)
	require.NoError(t, err, "existing conversation rejected")

	// 客服在夹具会话中回复后，访客可以再新建会话。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "您好，请问有什么可以帮您？",
	})
	require.NoError(t, err)
	created, err := send(nil)
	require.NoError(t, err)
	require.True(t, created.CreatedConversation)
	_, err = send(nil)
	require.ErrorAs(t, err, &conflict, "limit reached again")

	// 未回复的会话结束后不再计入，访客可以再新建会话。
	coordinator := newTestAgentRun(f.db, testEnqueuer, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.owner, created.Conversation.ID)
	require.NoError(t, err)
	result, err := send(nil)
	require.NoError(t, err)
	require.True(t, result.CreatedConversation)
}

// TestVisitorUploadRejectsMismatchedImage 验证按扩展名内嵌展示的图片内容与类型不符时不能完成上传。
func TestVisitorUploadRejectsMismatchedImage(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	record, err := customerchataction.NewCreateWebsiteVisitorUploadAction(f.db, testEnqueuer, localStorage).Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, FileName: "伪装.png", ContentType: "image/png", ByteSize: 15,
	})
	require.NoError(t, err)
	var validation *fileaction.ValidationError
	_, err = customerchataction.NewCompleteWebsiteVisitorUploadAction(f.db).Execute(ctx, f.channelID, websiteVisitorExternalID, record.ID,
		func(_ context.Context, file *servermodels.File) (serverfilecontent.UploadedObject, error) {
			return serverfilecontent.UploadedObject{ByteSize: file.ByteSize, Head: []byte("<html>script</html>")}, nil
		})
	require.ErrorAs(t, err, &validation)
	require.Equal(t, fileaction.ValidationContentTypeInvalid, validation.Fields["contentType"])
}
