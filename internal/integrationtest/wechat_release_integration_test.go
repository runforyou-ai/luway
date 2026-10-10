//go:build server

package integrationtest

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestWechatReleaseTest 验证全网发布检测应答：专用测试公众号的文本检测与事件检测得到加密的被动回复，客服消息检测先回空再经任务用授权码换取凭据后回复，测试公众号的消息不写入入站事件。
func TestWechatReleaseTest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	fake := &fakeWechatOpenPlatform{responses: map[string]string{}, calls: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	tasks := servertest.NewTasks()
	service := direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db), Wechat: client}, nil, nil, nil, tasks, nil, nil, nil)
	f := &wechatInboundFixture{db: db, fake: fake, httpAPI: api.NewService(service, api.WithWechatMessages(wechataction.NewReceiveMessageAction(db, tasks)))}
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "全网发布", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	meta := appservice.RequestMeta{Token: installed.Auth.Token, WorkspaceID: installed.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	fake.set("/cgi-bin/component/api_component_token", `{"component_access_token":"platform-token","expires_in":7200}`)
	_, err = service.SaveWechatPlatform(ctx, meta, appservice.WechatPlatformInput{
		ComponentAppID: testComponentAppID, ComponentAppSecret: "secret", Token: testComponentToken, EncodingAESKey: testComponentAESKey,
	})
	require.NoError(t, err)
	const testAppID, testUserName = "wx570bc396a51b8ff8", "gh_3c884a361561"
	path := "/public/wechat/accounts/" + testAppID + "/messages"
	message := func(content string) string {
		return strings.Replace(wechatUserMessage("o-tester", wechatText("9001", content)), "gh_test", testUserName, 1)
	}

	cipher, err := wechat.NewCipher(testComponentToken, testComponentAESKey, testComponentAppID)
	require.NoError(t, err)
	// passiveReply 推送检测消息，校验被动回复按平台凭据加密签名，返回发给检测用户的文本内容。
	passiveReply := func(push string) string {
		t.Helper()
		response := f.postWechat(t, path, testComponentToken, testComponentAppID, push)
		require.Equal(t, http.StatusOK, response.Code)
		var sealed struct {
			Encrypt      string `xml:"Encrypt"`
			MsgSignature string `xml:"MsgSignature"`
			TimeStamp    string `xml:"TimeStamp"`
			Nonce        string `xml:"Nonce"`
		}
		require.NoError(t, xml.Unmarshal(response.Body.Bytes(), &sealed))
		require.NoError(t, cipher.VerifyEncrypted(sealed.MsgSignature, sealed.TimeStamp, sealed.Nonce, sealed.Encrypt))
		plain, err := cipher.Decrypt(sealed.Encrypt)
		require.NoError(t, err)
		var reply struct {
			ToUserName   string `xml:"ToUserName"`
			FromUserName string `xml:"FromUserName"`
			MsgType      string `xml:"MsgType"`
			Content      string `xml:"Content"`
		}
		require.NoError(t, xml.Unmarshal(plain, &reply))
		require.Equal(t, "o-tester", reply.ToUserName)
		require.Equal(t, testUserName, reply.FromUserName)
		require.Equal(t, "text", reply.MsgType)
		return reply.Content
	}

	// 文本检测与事件检测得到约定内容的被动回复。
	require.Equal(t, "TESTCOMPONENT_MSG_TYPE_TEXT_callback", passiveReply(message(wechat.ReleaseTestText)))
	event := strings.Replace(wechatUserMessage("o-tester", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[LOCATION]]></Event>"), "gh_test", testUserName, 1)
	require.Equal(t, "LOCATIONfrom_callback", passiveReply(event))

	// 客服消息检测：先回空响应并投递任务，任务用授权码换取凭据后经客服消息接口回复。
	response := f.postWechat(t, path, testComponentToken, testComponentAppID, message("QUERY_AUTH_CODE:queryauthcode@@@abc"))
	require.Equal(t, http.StatusOK, response.Code)
	require.Empty(t, response.Body.String())
	replies := tasks.Queued(wechataction.ReplyReleaseTestActionName, "")
	require.Len(t, replies, 1)
	input := servertest.TaskPayload[wechataction.ReplyReleaseTestInput](t, replies[0])
	require.Equal(t, wechataction.ReplyReleaseTestInput{AppID: testAppID, OpenID: "o-tester", AuthorizationCode: "queryauthcode@@@abc"}, input)
	_, err = db.NewUpdate().TableExpr("wechat_platforms").Set("verify_ticket = 'ticket', verify_ticket_received_at = now()").Where("TRUE").Exec(ctx)
	require.NoError(t, err)
	fake.set(pathQueryAuth, queryAuthResponse(testAppID, "tester-token", "tester-refresh", 1))
	fake.set("/cgi-bin/message/custom/send", `{"errcode":0,"errmsg":"ok"}`)
	require.NoError(t, wechataction.NewReplyReleaseTestAction(db, client).Execute(ctx, input))
	require.Len(t, fake.requests(pathQueryAuth), 1)
	require.Contains(t, fake.requests(pathQueryAuth)[0], `"authorization_code":"queryauthcode@@@abc"`)
	require.Len(t, fake.requests("/cgi-bin/message/custom/send"), 1)
	require.JSONEq(t, `{"touser":"o-tester","msgtype":"text","text":{"content":"queryauthcode@@@abc_from_api"}}`, fake.requests("/cgi-bin/message/custom/send")[0])

	// 测试公众号的其他消息按约定回复 success，不写入入站事件。
	response = f.postWechat(t, path, testComponentToken, testComponentAppID, message("你好"))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "success", response.Body.String())
	require.Empty(t, tasks.Queued(channelinboundaction.ReceiveEventActionName, ""))
}
