//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// fakeWechatOpenPlatform 按接口路径返回设定的微信开放平台响应。
type fakeWechatOpenPlatform struct {
	mu        sync.Mutex
	responses map[string]string
	calls     map[string]int
	bodies    map[string][]string
}

// ServeHTTP 记录请求正文并返回请求路径对应的响应。
func (f *fakeWechatOpenPlatform) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[request.URL.Path]++
	if f.bodies == nil {
		f.bodies = map[string][]string{}
	}
	f.bodies[request.URL.Path] = append(f.bodies[request.URL.Path], string(body))
	_, _ = writer.Write([]byte(f.responses[request.URL.Path]))
}

// requests 返回接口路径收到的请求正文。
func (f *fakeWechatOpenPlatform) requests(path string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[path]...)
}

// set 设定接口路径之后的响应。
func (f *fakeWechatOpenPlatform) set(path, response string) {
	f.mu.Lock()
	f.responses[path] = response
	f.mu.Unlock()
}

// count 返回接口路径收到的请求数。
func (f *fakeWechatOpenPlatform) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

// 微信开放平台授权相关接口路径。
const (
	pathPreAuthCode     = "/cgi-bin/component/api_create_preauthcode"
	pathQueryAuth       = "/cgi-bin/component/api_query_auth"
	pathAuthorizerInfo  = "/cgi-bin/component/api_get_authorizer_info"
	pathAuthorizerToken = "/cgi-bin/component/api_authorizer_token"
)

// queryAuthResponse 返回换取授权信息的响应，permissions 为授予的权限集编号。
func queryAuthResponse(appID, accessToken, refreshToken string, permissions ...int) string {
	items := arr.Map(permissions, func(id int) string { return fmt.Sprintf(`{"funcscope_category":{"id":%d}}`, id) })
	return fmt.Sprintf(`{"authorization_info":{"authorizer_appid":%q,"authorizer_access_token":%q,"expires_in":7200,"authorizer_refresh_token":%q,"func_info":[%s]}}`,
		appID, accessToken, refreshToken, strings.Join(items, ","))
}

// authorizerInfoResponse 返回公众号资料响应。
func authorizerInfoResponse(name string, serviceType, verifyType int) string {
	return fmt.Sprintf(`{"authorizer_info":{"nick_name":%q,"head_img":"https://wx.qlogo.cn/head","principal_name":"测试主体","user_name":"gh_test","service_type_info":{"id":%d},"verify_type_info":{"id":%d}}}`,
		name, serviceType, verifyType)
}

// authorizationPage 请求授权发起页或回跳地址并返回响应。
func authorizationPage(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Accept-Language", "zh-CN")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// TestWechatAuthorization 验证授权接入渠道：部署须先配置第三方平台；发起授权生成发起页与回跳地址；只接受已认证服务号；回跳与授权成功通知各自完成同一意图；
// 授权方凭据刷新写回刷新令牌；授权更新与取消授权按通知时间判序；AppID 在授权接入渠道内唯一，与密钥接入渠道互不限制；更换平台后须重新授权。
func TestWechatAuthorization(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	fake := &fakeWechatOpenPlatform{responses: map[string]string{}, calls: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	tasks := servertest.NewTasks()
	deployment := servertest.NewDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment, Wechat: client}, nil, nil, nil, tasks, nil, nil, nil)
	service := backend
	httpAPI := api.NewService(service,
		api.WithWechatPlatformEvents(wechataction.NewReceivePlatformEventAction(db, tasks)),
		api.WithWechatAuthorization(wechataction.NewAuthorizationPageQuery(db, deployment.PublicURL), wechataction.NewCompleteAuthorizationAction(db, client)),
	)
	applyEvent := wechataction.NewApplyAuthorizationEventAction(db, client)
	ctx := context.Background()
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "公众号授权", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	meta := appservice.RequestMeta{Token: installed.Auth.Token, WorkspaceID: installed.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	// 部署尚未配置第三方平台时不能创建授权接入渠道。
	_, err = createWechatChannel(t, ctx, service, meta, domain.ChannelTypeWechatAuthorization, "未配置平台")
	servertest.RequireErrorMessage(t, err, i18n.ErrorWechatPlatformNotConfigured)

	// 配置平台并收到验证票据。
	fake.set("/cgi-bin/component/api_component_token", `{"component_access_token":"platform-token","expires_in":7200}`)
	_, err = service.SaveWechatPlatform(ctx, meta, appservice.WechatPlatformInput{
		ComponentAppID: testComponentAppID, ComponentAppSecret: "secret", Token: testComponentToken, EncodingAESKey: testComponentAESKey,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, postVerifyTicket(t, httpAPI, testComponentAppID, "ticket", testComponentToken).Code)

	channelID, err := createWechatChannel(t, ctx, service, meta, domain.ChannelTypeWechatAuthorization, "授权服务号")
	require.NoError(t, err)
	siblingID, err := createWechatChannel(t, ctx, service, meta, domain.ChannelTypeWechatAuthorization, "另一个授权服务号")
	require.NoError(t, err)
	keyChannelID := createKeyChannel(t, ctx, service, meta, "密钥服务号")
	appID := randomWechatAppID()

	// 尚未授权时只有平台配置状态，检测返回未授权。
	channel, err := service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.True(t, channel.Connection.PlatformConfigured)
	require.Nil(t, channel.Connection.Status)
	require.Empty(t, channel.Connection.AppID)
	_, err = service.CheckWechatAuthorizationChannelConnection(ctx, meta, channelID)
	servertest.RequireErrorMessage(t, err, i18n.ErrorWechatChannelNotAuthorized)

	// 发起授权返回发起页地址，发起页链接到带预授权码与回跳地址的微信授权页。
	fake.set(pathPreAuthCode, `{"pre_auth_code":"preauth-1","expires_in":1800}`)
	started, err := service.StartWechatAuthorization(ctx, meta, channelID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(started.URL, servertest.PublicURL+wechataction.AuthorizationPagePath), started.URL)
	path := strings.TrimPrefix(started.URL, servertest.PublicURL+"/api")
	page := authorizationPage(httpAPI, path)
	require.Equal(t, http.StatusOK, page.Code)
	body := page.Body.String()
	require.Contains(t, body, "https://mp.weixin.qq.com/cgi-bin/componentloginpage?")
	require.Contains(t, body, "pre_auth_code=preauth-1")
	require.Contains(t, body, url.QueryEscape(started.URL+"/callback"))
	require.NotContains(t, body, "biz_appid")
	require.Equal(t, http.StatusNotFound, authorizationPage(httpAPI, "/public/wechat/authorizations/unknown").Code)

	// 管理员未完成授权时回跳不带授权码。
	require.Equal(t, http.StatusBadRequest, authorizationPage(httpAPI, path+"/callback").Code)

	// 未认证的服务号不能授权，意图保留。
	fake.set(pathQueryAuth, queryAuthResponse(appID, "authorizer-token-1", "refresh-1", 1, 2))
	fake.set(pathAuthorizerInfo, authorizerInfoResponse("测试服务号", 2, -1))
	page = authorizationPage(httpAPI, path+"/callback?auth_code=code-1&expires_in=600")
	require.Equal(t, http.StatusConflict, page.Code)
	require.Contains(t, page.Body.String(), "只能授权已认证的服务号")

	// 已认证服务号回跳后完成授权：登记 AppID、保存资料与授权方凭据，并列出缺少的权限。
	fake.set(pathAuthorizerInfo, authorizerInfoResponse("测试服务号", 2, 0))
	page = authorizationPage(httpAPI, path+"/callback?auth_code=code-1&expires_in=600")
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	require.Contains(t, page.Body.String(), "测试服务号")
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	connection := channel.Connection
	require.Equal(t, appID, connection.AppID)
	require.Equal(t, new(domain.WechatAuthorizationActive), connection.Status)
	require.Equal(t, "测试服务号", connection.NickName)
	require.Equal(t, "gh_test", connection.UserName)
	require.Equal(t, []appservice.WechatPermission{domain.WechatPermissionMaterial}, connection.MissingPermissions)
	require.NotNil(t, connection.AccessTokenExpiresAt)
	token, err := wechataction.AuthorizationAccessToken(ctx, db, client, appID, false)
	require.NoError(t, err)
	require.Equal(t, "authorizer-token-1", token)
	require.Zero(t, fake.count(pathAuthorizerToken))

	// 完成后发起页失效；同一意图再次回跳或收到授权成功通知时确认同一结果。
	require.Equal(t, http.StatusNotFound, authorizationPage(httpAPI, path).Code)
	require.Equal(t, http.StatusOK, authorizationPage(httpAPI, path+"/callback?auth_code=code-1").Code)
	require.NoError(t, applyEvent.Execute(ctx, wechataction.AuthorizationEventInput{
		InfoType: wechat.InfoTypeAuthorized, AppID: appID, AuthorizationCode: "code-1", PreAuthCode: "preauth-1", CreateTime: time.Now().Unix(),
	}))

	// 授权方凭据到期后用刷新令牌获取，微信返回的新刷新令牌写回授权。
	_, err = db.NewUpdate().Model((*servermodels.WechatAccessToken)(nil)).Set("expires_at = now() - interval '1 minute'").
		Where("credential = 'authorization' AND app_id = ?", appID).Exec(ctx)
	require.NoError(t, err)
	fake.set(pathAuthorizerToken, `{"authorizer_access_token":"authorizer-token-2","expires_in":7200,"authorizer_refresh_token":"refresh-2"}`)
	token, err = wechataction.AuthorizationAccessToken(ctx, db, client, appID, false)
	require.NoError(t, err)
	require.Equal(t, "authorizer-token-2", token)
	authorization := &servermodels.WechatAuthorization{}
	require.NoError(t, db.NewSelect().Model(authorization).Where("channel_id = ?", channelID).Scan(ctx))
	require.Equal(t, "refresh-2", authorization.RefreshToken)

	// 授权更新通知经任务刷新权限；早于已记录授权时间的更新不生效。
	response := postComponentEvent(t, httpAPI, testComponentAppID, testComponentToken, fmt.Sprintf(
		"<CreateTime>%d</CreateTime><InfoType><![CDATA[updateauthorized]]></InfoType><AuthorizerAppid><![CDATA[%s]]></AuthorizerAppid><AuthorizationCode><![CDATA[code-2]]></AuthorizationCode>",
		time.Now().Unix(), appID))
	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, tasks.Queued(wechataction.ApplyAuthorizationEventActionName, ""), 1)
	fake.set(pathQueryAuth, queryAuthResponse(appID, "authorizer-token-3", "refresh-2", 1, 2, 11))
	require.NoError(t, applyEvent.Execute(ctx, wechataction.AuthorizationEventInput{
		InfoType: wechat.InfoTypeUpdateAuthorized, AppID: appID, AuthorizationCode: "code-2", CreateTime: time.Now().Unix(),
	}))
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Empty(t, channel.Connection.MissingPermissions)
	fake.set(pathQueryAuth, queryAuthResponse(appID, "authorizer-token-old", "refresh-2", 1))
	require.NoError(t, applyEvent.Execute(ctx, wechataction.AuthorizationEventInput{
		InfoType: wechat.InfoTypeUpdateAuthorized, AppID: appID, AuthorizationCode: "code-old", CreateTime: time.Now().Add(-time.Hour).Unix(),
	}))
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Empty(t, channel.Connection.MissingPermissions, "stale update applied")

	// 取消授权通知标记授权已取消并删除授权方凭据，与授权同一秒的取消也生效。
	revokedAt := time.Now().Unix()
	response = postComponentEvent(t, httpAPI, testComponentAppID, testComponentToken, fmt.Sprintf(
		"<CreateTime>%d</CreateTime><InfoType><![CDATA[unauthorized]]></InfoType><AuthorizerAppid><![CDATA[%s]]></AuthorizerAppid>", revokedAt, appID))
	require.Equal(t, http.StatusOK, response.Code)
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatAuthorizationRevoked), channel.Connection.Status)
	require.Nil(t, channel.Connection.AccessTokenExpiresAt)
	_, err = wechataction.AuthorizationAccessToken(ctx, db, client, appID, false)
	require.ErrorIs(t, err, wechataction.ErrNotAuthorized)
	_, err = service.CheckWechatAuthorizationChannelConnection(ctx, meta, channelID)
	servertest.RequireErrorMessage(t, err, i18n.ErrorWechatChannelNotAuthorized)

	// 不晚于取消时间的授权成功通知不恢复授权。
	fake.set(pathPreAuthCode, `{"pre_auth_code":"preauth-2","expires_in":1800}`)
	_, err = service.StartWechatAuthorization(ctx, meta, channelID)
	require.NoError(t, err)
	fake.set(pathQueryAuth, queryAuthResponse(appID, "authorizer-token-4", "refresh-3", 1, 2, 11))
	require.NoError(t, applyEvent.Execute(ctx, wechataction.AuthorizationEventInput{
		InfoType: wechat.InfoTypeAuthorized, AppID: appID, AuthorizationCode: "code-3", PreAuthCode: "preauth-2", CreateTime: revokedAt,
	}))
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatAuthorizationRevoked), channel.Connection.Status, "stale authorized event")

	// 同一秒内取消后才回跳的授权不生效，回跳页提示授权已取消。
	fake.set(pathPreAuthCode, `{"pre_auth_code":"preauth-5","expires_in":1800}`)
	started, err = service.StartWechatAuthorization(ctx, meta, channelID)
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*servermodels.WechatAuthorization)(nil)).Set("revoked_at = date_trunc('second', now()) + interval '1 hour'").
		Where("channel_id = ?", channelID).Exec(ctx)
	require.NoError(t, err)
	page = authorizationPage(httpAPI, strings.TrimPrefix(started.URL, servertest.PublicURL+"/api")+"/callback?auth_code=code-6")
	require.Equal(t, http.StatusConflict, page.Code)
	require.Contains(t, page.Body.String(), "公众号已取消授权")

	// 重新授权时发起页只允许渠道已连接的公众号，回跳后授权恢复有效。
	fake.set(pathPreAuthCode, `{"pre_auth_code":"preauth-3","expires_in":1800}`)
	started, err = service.StartWechatAuthorization(ctx, meta, channelID)
	require.NoError(t, err)
	path = strings.TrimPrefix(started.URL, servertest.PublicURL+"/api")
	require.Contains(t, authorizationPage(httpAPI, path).Body.String(), "biz_appid="+appID)
	_, err = db.NewUpdate().Model((*servermodels.WechatAuthorization)(nil)).
		Set("authorized_at = now() - interval '2 seconds'").Set("revoked_at = now() - interval '1 second'").
		Where("channel_id = ?", channelID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, authorizationPage(httpAPI, path+"/callback?auth_code=code-4").Code)
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatAuthorizationActive), channel.Connection.Status)
	require.Nil(t, channel.Connection.RevokedAt)

	// 同一公众号不能授权给另一个授权接入渠道；授权接入渠道启用时密钥接入渠道须先停用才能连接该公众号，连接后不能再启用。
	fake.set(pathPreAuthCode, `{"pre_auth_code":"preauth-4","expires_in":1800}`)
	started, err = service.StartWechatAuthorization(ctx, meta, siblingID)
	require.NoError(t, err)
	page = authorizationPage(httpAPI, strings.TrimPrefix(started.URL, servertest.PublicURL+"/api")+"/callback?auth_code=code-5")
	require.Equal(t, http.StatusConflict, page.Code)
	require.Contains(t, page.Body.String(), "该公众号已连接到其他渠道")
	fake.set("/cgi-bin/stable_token", `{"access_token":"key-token","expires_in":7200}`)
	keyInput := appservice.WechatKeyConnectionInput{
		AppID: appID, AppSecret: "app-secret", Token: "callbacktoken", EncryptionMode: domain.WechatEncryptionPlain,
	}
	_, err = service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, keyInput)
	require.ErrorContains(t, err, "另一种接入方式")
	_, err = service.DeactivateMessageChannel(ctx, meta, keyChannelID)
	require.NoError(t, err)
	_, err = service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, keyInput)
	require.NoError(t, err)
	_, err = service.ActivateMessageChannel(ctx, meta, keyChannelID)
	require.ErrorContains(t, err, "另一种接入方式")
	keyToken, err := wechataction.KeyAccessToken(ctx, db, client, appID, false)
	require.NoError(t, err)
	require.Equal(t, "key-token", keyToken)
	token, err = wechataction.AuthorizationAccessToken(ctx, db, client, appID, false)
	require.NoError(t, err)
	require.Equal(t, "authorizer-token-4", token)

	// 更换第三方平台后授权须重新授权。
	_, err = db.NewUpdate().Model((*servermodels.WechatPlatform)(nil)).Set("component_app_id = 'wx00000000000000cc'").Where("true").Exec(ctx)
	require.NoError(t, err)
	channel, err = service.GetWechatAuthorizationChannel(ctx, meta, channelID)
	require.NoError(t, err)
	require.Equal(t, new(domain.WechatAuthorizationPlatformChanged), channel.Connection.Status)
	_, err = wechataction.AuthorizationAccessToken(ctx, db, client, appID, true)
	require.ErrorIs(t, err, wechataction.ErrNotAuthorized)
}
