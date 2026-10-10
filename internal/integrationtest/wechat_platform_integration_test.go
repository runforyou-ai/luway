//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

const (
	testComponentAppID  = "wx00000000000000aa"
	testComponentToken  = "componenttoken"
	testComponentAESKey = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
)

// fakeWechatPlatform 模拟微信平台凭据接口：记录调用次数与收到的票据，按设定返回凭据或错误码，可设定响应延迟。
type fakeWechatPlatform struct {
	mu       sync.Mutex
	calls    atomic.Int32
	tickets  []string
	response string
	delay    time.Duration
}

// ServeHTTP 返回设定的平台凭据响应。
func (f *fakeWechatPlatform) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.calls.Add(1)
	f.mu.Lock()
	response, delay := f.response, f.delay
	f.mu.Unlock()
	body, _ := io.ReadAll(request.Body)
	if index := strings.Index(string(body), `"component_verify_ticket":"`); index >= 0 {
		rest := string(body)[index+len(`"component_verify_ticket":"`):]
		f.mu.Lock()
		f.tickets = append(f.tickets, rest[:strings.Index(rest, `"`)])
		f.mu.Unlock()
	}
	time.Sleep(delay)
	_, _ = writer.Write([]byte(response))
}

// set 设定之后的响应与延迟。
func (f *fakeWechatPlatform) set(response string, delay time.Duration) {
	f.mu.Lock()
	f.response, f.delay = response, delay
	f.mu.Unlock()
}

// postVerifyTicket 以第三方平台凭据加密验证票据通知并发送到授权事件接收地址，返回响应。
func postVerifyTicket(t *testing.T, handler http.Handler, appID, ticket, signatureToken string) *httptest.ResponseRecorder {
	t.Helper()
	return postComponentEvent(t, handler, appID, signatureToken, fmt.Sprintf(
		"<InfoType><![CDATA[component_verify_ticket]]></InfoType><CreateTime>%d</CreateTime><ComponentVerifyTicket><![CDATA[%s]]></ComponentVerifyTicket>",
		time.Now().Unix(), ticket))
}

// postComponentEvent 以第三方平台凭据加密通知字段并发送到授权事件接收地址，返回响应。
func postComponentEvent(t *testing.T, handler http.Handler, appID, signatureToken, fields string) *httptest.ResponseRecorder {
	t.Helper()
	cipher, err := wechat.NewCipher(testComponentToken, testComponentAESKey, appID)
	require.NoError(t, err)
	encrypted, err := cipher.Encrypt([]byte(fmt.Sprintf("<xml><AppId><![CDATA[%s]]></AppId>%s</xml>", appID, fields)))
	require.NoError(t, err)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := wechat.Signature(signatureToken, timestamp, "nonce", encrypted)
	body := fmt.Sprintf("<xml><AppId><![CDATA[%s]]></AppId><Encrypt><![CDATA[%s]]></Encrypt></xml>", appID, encrypted)
	request := httptest.NewRequest(http.MethodPost, "/public/wechat/events?timestamp="+timestamp+"&nonce=nonce&encrypt_type=aes&msg_signature="+signature, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// countRefreshTasks 返回登记器中待执行的平台凭据刷新任务数。
func countRefreshTasks(t *testing.T, tasks *servertest.Tasks) int {
	t.Helper()
	return len(tasks.Queued(wechataction.RefreshPlatformTokenActionName, ""))
}

// TestWechatPlatform 验证第三方平台配置校验、验证票据回调、平台状态、平台凭据刷新与失败记录、并发刷新只请求一次、过期后定时续期，以及更换 Component AppID 时清空票据与原凭据且进行中的刷新不写回。
func TestWechatPlatform(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	fake := &fakeWechatPlatform{response: `{"component_access_token":"platform-token-1","expires_in":7200}`}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	tasks := servertest.NewTasks()
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db), Wechat: client},
		nil, nil, nil, tasks, nil, nil, nil)
	service := backend
	httpAPI := api.NewService(service, api.WithWechatPlatformEvents(wechataction.NewReceivePlatformEventAction(db, tasks)))
	refresh := wechataction.NewRefreshPlatformTokenAction(db, client)
	ctx := context.Background()
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "微信平台", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: installed.Auth.Token, Locale: domain.LocaleChineseSimplified}

	// 未配置时只返回接入地址，通知返回 404。
	platform, err := service.GetWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.False(t, platform.Configured)
	require.Nil(t, platform.Status)
	require.Equal(t, servertest.PublicURL+"/api/public/wechat/events", platform.EventURL)
	require.Equal(t, "app.example.test", platform.AuthorizationDomain)
	require.Equal(t, http.StatusNotFound, postVerifyTicket(t, httpAPI, testComponentAppID, "ticket-0", testComponentToken).Code, "unconfigured event")

	// 凭据格式错误时返回字段校验错误。
	_, err = service.SaveWechatPlatform(ctx, adminMeta, appservice.WechatPlatformInput{ComponentAppID: "wx123", Token: "a", EncodingAESKey: "short"})
	servertest.RequireErrorMessage(t, err, i18n.ErrorValidationFailed)

	// 保存后尚无验证票据，不请求平台凭据。
	input := appservice.WechatPlatformInput{
		ComponentAppID: testComponentAppID, ComponentAppSecret: "secret-1", Token: testComponentToken, EncodingAESKey: testComponentAESKey,
	}
	platform, err = service.SaveWechatPlatform(ctx, adminMeta, input)
	require.NoError(t, err)
	require.True(t, platform.Configured)
	require.Equal(t, testComponentAppID, platform.ComponentAppID)
	require.Nil(t, platform.VerifyTicketReceivedAt)
	require.Equal(t, int32(0), fake.calls.Load())
	require.Equal(t, new(domain.WechatPlatformStatusWaitingTicket), platform.Status)

	// 签名错误的通知被拒绝。
	require.Equal(t, http.StatusUnauthorized, postVerifyTicket(t, httpAPI, testComponentAppID, "ticket-x", "othertoken").Code, "bad signature")

	// 验证票据写入配置并投递刷新任务，任务获取平台凭据。
	response := postVerifyTicket(t, httpAPI, testComponentAppID, "ticket-1", testComponentToken)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "success", response.Body.String())
	require.Equal(t, 1, countRefreshTasks(t, tasks))
	platform, err = service.GetWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatPlatformStatusPending), platform.Status)
	require.NoError(t, refresh.Execute(ctx, wechataction.RefreshPlatformTokenInput{}))
	platform, err = service.GetWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, platform.VerifyTicketReceivedAt)
	require.NotNil(t, platform.AccessTokenExpiresAt)
	require.Nil(t, platform.TokenFailure)
	require.Equal(t, new(domain.WechatPlatformStatusReady), platform.Status)
	require.Equal(t, int32(1), fake.calls.Load())
	require.Equal(t, "ticket-1", fake.tickets[0])

	// 平台凭据仍有充足有效期时，新的验证票据不投递刷新。
	tasks.Take(wechataction.RefreshPlatformTokenActionName, "")
	require.Equal(t, http.StatusOK, postVerifyTicket(t, httpAPI, testComponentAppID, "ticket-2", testComponentToken).Code, "second ticket")
	require.Equal(t, 0, countRefreshTasks(t, tasks), "refresh tasks with fresh token")

	// 出口 IP 不在白名单时记录失败原因与微信识别的 IP，原凭据保留。
	fake.set(`{"errcode":61004,"errmsg":"access clientip is not registered requestIP: 203.0.113.9 rid: 1"}`, 0)
	platform, err = service.CheckWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatTokenFailureIPNotWhitelisted), platform.TokenFailure)
	require.Equal(t, new(domain.WechatPlatformStatusFailed), platform.Status)
	require.Equal(t, "203.0.113.9", platform.TokenFailureDetail)
	require.NotNil(t, platform.TokenFailedAt)
	require.NotNil(t, platform.AccessTokenExpiresAt)

	// 两个并发的重新检测只请求一次微信，等待方采用持有租约一方写回的失败。
	fake.set(`{"errcode":40013,"errmsg":"invalid appid"}`, 300*time.Millisecond)
	before := fake.calls.Load()
	checkErrs := make([]error, 2)
	var checks sync.WaitGroup
	for index := range checkErrs {
		checks.Add(1)
		go func() {
			defer checks.Done()
			_, checkErrs[index] = wechataction.PlatformAccessToken(ctx, db, client, true)
		}()
	}
	checks.Wait()
	require.ErrorIs(t, checkErrs[0], wechataction.ErrTokenFailed)
	require.ErrorIs(t, checkErrs[1], wechataction.ErrTokenFailed)
	require.Equal(t, int32(1), fake.calls.Load()-before, "concurrent check calls")

	// 凭据已过期时，两个并发获取只请求一次微信，未持有租约的一方等待并采用其结果。
	fake.set(`{"component_access_token":"platform-token-2","expires_in":7200}`, 300*time.Millisecond)
	_, err = db.NewUpdate().Model((*servermodels.WechatAccessToken)(nil)).
		Set("expires_at = now() - interval '1 minute'").Where("app_id = ?", testComponentAppID).Exec(ctx)
	require.NoError(t, err)
	before = fake.calls.Load()
	tokens := make([]string, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range tokens {
		wait.Add(1)
		go func() {
			defer wait.Done()
			tokens[index], errs[index] = wechataction.PlatformAccessToken(ctx, db, client, false)
		}()
	}
	wait.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, []string{"platform-token-2", "platform-token-2"}, tokens)
	require.Equal(t, int32(1), fake.calls.Load()-before, "concurrent token calls")
	platform, err = service.GetWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.Nil(t, platform.TokenFailure)
	require.Equal(t, new(domain.WechatPlatformStatusReady), platform.Status)

	// 凭据过期且没有失败记录时状态为待获取，定时续期用已保存的票据重新获取。
	_, err = db.NewUpdate().Model((*servermodels.WechatAccessToken)(nil)).
		Set("expires_at = now() - interval '1 minute'").Where("app_id = ?", testComponentAppID).Exec(ctx)
	require.NoError(t, err)
	fake.set(`{"component_access_token":"platform-token-renewed","expires_in":7200}`, 0)
	platform, err = service.GetWechatPlatform(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatPlatformStatusPending), platform.Status, "expired platform")
	require.NoError(t, refresh.Execute(ctx, wechataction.RefreshPlatformTokenInput{}))
	token, err := wechataction.PlatformAccessToken(ctx, db, client, false)
	require.NoError(t, err)
	require.Equal(t, "platform-token-renewed", token)

	// 更换 Component AppID 时清空验证票据与原平台凭据，进行中的原平台刷新不写回凭据，新平台等待票据。
	fake.set(`{"component_access_token":"platform-token-3","expires_in":7200}`, 500*time.Millisecond)
	inFlight := make(chan error, 1)
	go func() {
		_, err := wechataction.PlatformAccessToken(ctx, db, client, true)
		inFlight <- err
	}()
	time.Sleep(150 * time.Millisecond)
	input.ComponentAppID = "wx00000000000000bb"
	platform, err = service.SaveWechatPlatform(ctx, adminMeta, input)
	require.ErrorIs(t, <-inFlight, wechataction.ErrTokenSuperseded, "in-flight refresh")
	fake.set(`{"component_access_token":"platform-token-3","expires_in":7200}`, 0)
	before = fake.calls.Load()
	require.NoError(t, err)
	require.Equal(t, "wx00000000000000bb", platform.ComponentAppID)
	require.Nil(t, platform.VerifyTicketReceivedAt)
	require.Nil(t, platform.AccessTokenExpiresAt)
	require.Equal(t, new(domain.WechatPlatformStatusWaitingTicket), platform.Status)
	exists, err := db.NewSelect().Model((*servermodels.WechatAccessToken)(nil)).Where("app_id = ?", testComponentAppID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "old token exists")
	require.Equal(t, http.StatusUnauthorized, postVerifyTicket(t, httpAPI, testComponentAppID, "ticket-old", testComponentToken).Code, "old platform ticket")

	// 同一 AppID 更换 AppSecret 时保留验证票据并立即用新凭据获取平台凭据。
	require.Equal(t, http.StatusOK, postVerifyTicket(t, httpAPI, "wx00000000000000bb", "ticket-3", testComponentToken).Code, "new platform ticket")
	input.ComponentAppSecret = "secret-2"
	platform, err = service.SaveWechatPlatform(ctx, adminMeta, input)
	require.NoError(t, err)
	require.NotNil(t, platform.VerifyTicketReceivedAt)
	require.NotNil(t, platform.AccessTokenExpiresAt)
	require.Equal(t, before+1, fake.calls.Load())
}
