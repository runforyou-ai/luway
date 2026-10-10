//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// wechatInboundFixture 是公众号消息推送测试的数据库、HTTP 入口与入站事件处理。
type wechatInboundFixture struct {
	db       *bun.DB
	fake     *fakeWechatOpenPlatform
	httpAPI  http.Handler
	receiver *channelinboundaction.ReceiveEventAction
	tasks    *servertest.Tasks
}

// wechatUserMessage 返回 openID 当前发出的用户消息 XML，fields 是消息类型相关字段。
func wechatUserMessage(openID, fields string) string {
	return wechatUserMessageAt(openID, fields, time.Now())
}

// wechatUserMessageAt 返回 openID 在 at 发出的用户消息 XML。
func wechatUserMessageAt(openID, fields string, at time.Time) string {
	return fmt.Sprintf("<xml><ToUserName><![CDATA[gh_test]]></ToUserName><FromUserName><![CDATA[%s]]></FromUserName><CreateTime>%d</CreateTime>%s</xml>",
		openID, at.Unix(), fields)
}

// wechatText 返回编号为 msgID 的文本消息字段。
func wechatText(msgID, content string) string {
	return fmt.Sprintf("<MsgType><![CDATA[text]]></MsgType><Content><![CDATA[%s]]></Content><MsgId>%s</MsgId>", content, msgID)
}

// postWechat 向 path 发送推送：encryptAppID 非空时按 token、EncodingAESKey 与接收方 AppID 加密，否则以明文签名发送。
func (f *wechatInboundFixture) postWechat(t *testing.T, path, token, encryptAppID, message string) *httptest.ResponseRecorder {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	query := "?timestamp=" + timestamp + "&nonce=nonce&signature=" + wechat.Signature(token, timestamp, "nonce")
	body := message
	if encryptAppID != "" {
		cipher, err := wechat.NewCipher(token, testComponentAESKey, encryptAppID)
		require.NoError(t, err)
		encrypted, err := cipher.Encrypt([]byte(message))
		require.NoError(t, err)
		query += "&encrypt_type=aes&msg_signature=" + wechat.Signature(token, timestamp, "nonce", encrypted)
		body = fmt.Sprintf("<xml><ToUserName><![CDATA[gh_test]]></ToUserName><Encrypt><![CDATA[%s]]></Encrypt></xml>", encrypted)
	}
	request := httptest.NewRequest(http.MethodPost, path+query, strings.NewReader(body))
	response := httptest.NewRecorder()
	f.httpAPI.ServeHTTP(response, request)
	return response
}

// processEvents 取走并执行排队中的入站事件任务，返回执行的任务数。
func (f *wechatInboundFixture) processEvents(t *testing.T) int {
	t.Helper()
	runs := f.tasks.Take(channelinboundaction.ReceiveEventActionName, "")
	for _, run := range runs {
		require.NoError(t, f.receiver.Execute(context.Background(), servertest.TaskPayload[channelinboundaction.ReceiveEventInput](t, run)))
	}
	return len(runs)
}

// identityMessages 返回渠道中 openID 的渠道身份所属联系人与其会话消息的类型和正文。
func (f *wechatInboundFixture) identityMessages(t *testing.T, channelID, openID string) (string, []string) {
	t.Helper()
	ctx := context.Background()
	var contactID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_identities").ColumnExpr("contact_id::text").
		Where("channel_id = ? AND external_id = ?", channelID, openID).Scan(ctx, &contactID))
	var messages []string
	require.NoError(t, f.db.NewSelect().TableExpr("messages AS msg").ColumnExpr("msg.type || ':' || msg.body").
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Where("ci.channel_id = ? AND ci.external_id = ? AND msg.type <> ?", channelID, openID, domain.MessageTypeSystem).
		OrderExpr("msg.message_seq").Scan(ctx, &messages))
	return contactID, messages
}

// TestWechatInbound 验证公众号消息推送：密钥接入渠道的服务器地址验证在 Token 变更后重置；安全与明文模式推送验签后写入入站事件任务并按消息编号排重；
// 文本、位置与链接写成文本，图片经临时素材接口取回为附件，事件不写入；停用渠道丢弃推送；授权接入推送只在当前平台下授权有效且接收方为该公众号时接收；同一公众号两种渠道中的同一 OpenID 顺序或并发首次入站时都对应同一联系人。
func TestWechatInbound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	fake := &fakeWechatOpenPlatform{responses: map[string]string{}, calls: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	tasks := servertest.NewTasks()
	deployment := servertest.NewDeployment(t, db)
	service := direct.New(db, direct.DeploymentConfig{Deployment: deployment, Wechat: client}, nil, nil, nil, tasks, nil, nil, nil)
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeWechatKey, wechataction.NewAdapter(db, client, domain.ChannelTypeWechatKey))
	adapters.Register(domain.ChannelTypeWechatAuthorization, wechataction.NewAdapter(db, client, domain.ChannelTypeWechatAuthorization))
	local, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	writer := serverfilecontent.NewWriter(local, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} })
	scheduler := &countingAgentScheduler{}
	retrieve := channelinboundaction.NewRetrieveMediaAction(db, adapters, writer, scheduler)
	f := &wechatInboundFixture{
		db: db, fake: fake,
		httpAPI: api.NewService(service,
			api.WithWechatPlatformEvents(wechataction.NewReceivePlatformEventAction(db, tasks)),
			api.WithWechatMessages(wechataction.NewReceiveMessageAction(db, tasks)),
		),
		receiver: channelinboundaction.NewReceiveEventAction(db, adapters, scheduler, localStorage, tasks, retrieve, func() string { return servertest.PublicURL }),
		tasks:    tasks,
	}
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "公众号消息", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	meta := appservice.RequestMeta{Token: installed.Auth.Token, WorkspaceID: installed.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	// 连接密钥接入渠道，渠道详情给出服务器地址。
	appID := randomWechatAppID()
	keyChannelID := createKeyChannel(t, ctx, service, meta, "密钥服务号")
	fake.set("/cgi-bin/stable_token", `{"access_token":"key-token","expires_in":7200}`)
	input := appservice.WechatKeyConnectionInput{AppID: appID, AppSecret: "secret", Token: "keytoken", EncryptionMode: domain.WechatEncryptionSafe, EncodingAESKey: testComponentAESKey}
	keyChannel, err := service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, input)
	require.NoError(t, err)
	keyPath := "/public/wechat/channels/" + keyChannelID + "/messages"
	require.Equal(t, servertest.PublicURL+"/api"+keyPath, keyChannel.Connection.ServerURL)
	require.Nil(t, keyChannel.Connection.ServerVerifiedAt)

	// 服务器地址验证：签名错误返回 401，未知渠道返回 404，通过后原样返回 echostr 并记录验证时间。
	verify := func(path, token string) *httptest.ResponseRecorder {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		request := httptest.NewRequest(http.MethodGet, path+"?timestamp="+timestamp+"&nonce=n&echostr=echo-1&signature="+wechat.Signature(token, timestamp, "n"), nil)
		response := httptest.NewRecorder()
		f.httpAPI.ServeHTTP(response, request)
		return response
	}
	require.Equal(t, http.StatusUnauthorized, verify(keyPath, "othertoken").Code)
	require.Equal(t, http.StatusNotFound, verify("/public/wechat/channels/"+createKeyChannel(t, ctx, service, meta, "未连接")+"/messages", "keytoken").Code)
	verified := verify(keyPath, "keytoken")
	require.Equal(t, http.StatusOK, verified.Code)
	require.Equal(t, "echo-1", verified.Body.String())
	keyChannel, err = service.GetWechatKeyChannel(ctx, meta, keyChannelID)
	require.NoError(t, err)
	require.NotNil(t, keyChannel.Connection.ServerVerifiedAt)

	// 保存相同 Token 时保留验证结果，更换 Token 后等待重新验证。
	keyChannel, err = service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, input)
	require.NoError(t, err)
	require.NotNil(t, keyChannel.Connection.ServerVerifiedAt)
	input.Token = "keytoken2"
	keyChannel, err = service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, input)
	require.NoError(t, err)
	require.Nil(t, keyChannel.Connection.ServerVerifiedAt)

	// 安全模式推送：签名错误返回 401；有效推送回复 success，重推不重复写入任务。
	require.Equal(t, http.StatusUnauthorized, f.postWechat(t, keyPath, "keytoken", appID, wechatUserMessage("o-user-1", wechatText("1001", "你好"))).Code)
	text := wechatUserMessage("o-user-1", wechatText("1001", "你好"))
	response := f.postWechat(t, keyPath, "keytoken2", appID, text)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "success", response.Body.String())
	require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", appID, text).Code)
	// 关注事件写入任务开启回复窗口，其他事件不写入任务。
	require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", appID, wechatUserMessage("o-user-1", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[subscribe]]></Event>")).Code)
	require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", appID, wechatUserMessage("o-user-1", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[VIEW]]></Event>")).Code)
	require.Equal(t, 2, f.processEvents(t))

	// 位置、链接写成文本，图片取回为附件。
	fake.set("/cgi-bin/media/get", "\xff\xd8\xff\xe0\x00\x10JFIF\x00image")
	for _, fields := range []string{
		"<MsgType><![CDATA[location]]></MsgType><Location_X>23.134521</Location_X><Location_Y>113.358803</Location_Y><Label><![CDATA[广州塔]]></Label><MsgId>1002</MsgId>",
		"<MsgType><![CDATA[link]]></MsgType><Title><![CDATA[订单]]></Title><Description><![CDATA[查询]]></Description><Url><![CDATA[https://example.test/o]]></Url><MsgId>1003</MsgId>",
		"<MsgType><![CDATA[image]]></MsgType><PicUrl><![CDATA[https://example.test/p]]></PicUrl><MediaId><![CDATA[media-1]]></MediaId><MsgId>1004</MsgId>",
	} {
		require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", appID, wechatUserMessage("o-user-1", fields)).Code)
	}
	require.Equal(t, 3, f.processEvents(t))
	keyContactID, messages := f.identityMessages(t, keyChannelID, "o-user-1")
	require.Equal(t, []string{"text:你好", "text:广州塔 (23.134521, 113.358803)", "text:订单\n查询\nhttps://example.test/o", "attachment:"}, messages)
	var attachment struct {
		Status      string `bun:"transfer_status"`
		ContentType string `bun:"content_type"`
	}
	require.NoError(t, db.NewSelect().TableExpr("message_attachments AS ma").Column("ma.transfer_status", "ma.content_type").
		Join("JOIN messages AS msg ON msg.id = ma.message_id").
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Where("ci.channel_id = ?", keyChannelID).Scan(ctx, &attachment))
	require.Equal(t, string(domain.MessageAttachmentTransferReady), attachment.Status)
	require.Equal(t, "image/jpeg", attachment.ContentType)
	require.Equal(t, 1, fake.count("/cgi-bin/media/get"))

	// 明文模式按 signature 校验原文。
	input.EncryptionMode, input.EncodingAESKey = domain.WechatEncryptionPlain, ""
	_, err = service.SaveWechatKeyChannelConnection(ctx, meta, keyChannelID, input)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", "", wechatUserMessage("o-user-2", wechatText("1005", "明文"))).Code)
	require.Equal(t, 1, f.processEvents(t))
	_, messages = f.identityMessages(t, keyChannelID, "o-user-2")
	require.Equal(t, []string{"text:明文"}, messages)

	// 停用渠道后推送照常回复 success，不写入任务。
	_, err = db.NewUpdate().TableExpr("channels").Set("enabled = false").Where("id = ?", keyChannelID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.postWechat(t, keyPath, "keytoken2", "", wechatUserMessage("o-user-2", wechatText("1006", "停用"))).Code)
	require.Zero(t, f.processEvents(t))

	// 尚未配置第三方平台时授权接入推送返回 404。
	authorizationPath := "/public/wechat/accounts/" + appID + "/messages"
	require.Equal(t, http.StatusNotFound, f.postWechat(t, authorizationPath, testComponentToken, testComponentAppID, wechatUserMessage("o-user-1", wechatText("2001", "授权"))).Code)

	// 配置平台后为同一公众号建立授权接入渠道。
	fake.set("/cgi-bin/component/api_component_token", `{"component_access_token":"platform-token","expires_in":7200}`)
	_, err = service.SaveWechatPlatform(ctx, meta, appservice.WechatPlatformInput{
		ComponentAppID: testComponentAppID, ComponentAppSecret: "secret", Token: testComponentToken, EncodingAESKey: testComponentAESKey,
	})
	require.NoError(t, err)
	authorizationChannelID, err := createWechatChannel(t, ctx, service, meta, domain.ChannelTypeWechatAuthorization, "授权服务号")
	require.NoError(t, err)
	_, err = db.NewUpdate().TableExpr("channels").Set("provider_account_id = ?", appID).Where("id = ?", authorizationChannelID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw(`INSERT INTO wechat_authorizations (channel_id, workspace_id, component_app_id, refresh_token, permission_ids, nick_name, head_image_url, principal_name, user_name, authorized_at)
		VALUES (?, ?, ?, 'refresh', '{1,2,11}', '服务号', '', '主体', 'gh_test', now())`, authorizationChannelID, installed.Workspace.ID, testComponentAppID).Exec(ctx)
	require.NoError(t, err)

	// 第三方平台转发的推送按平台凭据解密，同一 OpenID 在授权接入渠道中对应同一联系人；未授权的公众号照常回复 success。
	require.Equal(t, http.StatusUnauthorized, f.postWechat(t, authorizationPath, "othertoken", testComponentAppID, wechatUserMessage("o-user-1", wechatText("2001", "授权"))).Code)
	require.Equal(t, http.StatusOK, f.postWechat(t, authorizationPath, testComponentToken, testComponentAppID, wechatUserMessage("o-user-1", wechatText("2001", "授权"))).Code)
	require.Equal(t, http.StatusOK, f.postWechat(t, "/public/wechat/accounts/"+randomWechatAppID()+"/messages", testComponentToken, testComponentAppID, wechatUserMessage("o-user-1", wechatText("2002", "其他"))).Code)
	// 接收方不是该公众号原始 ID 的推送不写入。
	otherReceiver := strings.Replace(wechatUserMessage("o-user-1", wechatText("2005", "其他公众号")), "gh_test", "gh_other", 1)
	require.Equal(t, http.StatusOK, f.postWechat(t, authorizationPath, testComponentToken, testComponentAppID, otherReceiver).Code)
	require.Equal(t, 1, f.processEvents(t))
	authorizationContactID, messages := f.identityMessages(t, authorizationChannelID, "o-user-1")
	require.Equal(t, []string{"text:授权"}, messages)
	require.Equal(t, keyContactID, authorizationContactID)

	// 取消授权或更换平台后丢弃推送。
	_, err = db.NewUpdate().TableExpr("wechat_authorizations").Set("revoked_at = now()").Where("channel_id = ?", authorizationChannelID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.postWechat(t, authorizationPath, testComponentToken, testComponentAppID, wechatUserMessage("o-user-1", wechatText("2003", "已取消"))).Code)
	_, err = db.NewUpdate().TableExpr("wechat_authorizations").Set("revoked_at = NULL, component_app_id = 'wx00000000000000bb'").Where("channel_id = ?", authorizationChannelID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.postWechat(t, authorizationPath, testComponentToken, testComponentAppID, wechatUserMessage("o-user-1", wechatText("2004", "平台已更换"))).Code)
	require.Zero(t, f.processEvents(t))

	// 同一公众号最多一种接入渠道启用：授权接入渠道启用时不能启用密钥接入渠道。
	_, err = service.ActivateMessageChannel(ctx, meta, keyChannelID)
	require.ErrorContains(t, err, "另一种接入方式")

	// 两种渠道同时为同一 OpenID 首次建立渠道身份时归入同一联系人。
	var wait sync.WaitGroup
	start := make(chan struct{})
	contactIDs := make([]string, 2)
	for index, channelID := range []string{keyChannelID, authorizationChannelID} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			assert.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				ensured, err := contactaction.EnsureChannelIdentity(ctx, tx, contactaction.EnsureChannelIdentityInput{
					WorkspaceID: installed.Workspace.ID, ChannelID: channelID, ExternalID: "o-user-3",
					ContactID: uuid.NewV7().String(), IdentityID: uuid.NewV7().String(),
				})
				if err == nil {
					contactIDs[index] = ensured.Contact.ID
				}
				return err
			}))
		}()
	}
	close(start)
	wait.Wait()
	keyContactID, authorizationContactID = contactIDs[0], contactIDs[1]
	require.NotEmpty(t, keyContactID)
	require.Equal(t, keyContactID, authorizationContactID)
}
