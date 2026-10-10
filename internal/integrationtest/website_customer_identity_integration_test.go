//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// customerRequest 以签名身份执行一次网站访客公开请求，返回状态、响应体与响应头。
func customerRequest(t *testing.T, service http.Handler, method, path, customerToken, body string) (int, map[string]any, http.Header) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Customer-Token", customerToken)
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	request.Header.Set("CF-IPCountry", "jp")
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	payload := map[string]any{}
	if response.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "解析公开响应 %s", response.Body.String())
	}
	return response.Code, payload, response.Header()
}

// signCustomer 以企业客户身份密钥签发测试用签名身份。
func signCustomer(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return token
}

// TestWebsiteCustomerIdentityHTTP 验证签名身份的验签与失效、登录用户联系人的关联与跨渠道复用（含附件先行）、签名邮箱补充，以及访客上下文按周期保存。
func TestWebsiteCustomerIdentityHTTP(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	scheduler := agentrunaction.NewScheduler(testEnqueuer)
	visitorService := direct.NewWebsiteVisitorBackend(f.db, scheduler, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	application := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := api.ClientOriginMiddleware("", "CF-IPCountry")(api.NewService(application, api.WithWebsiteVisitor(visitorService, func() bool { return false })))
	messengerPath := "/public/website-channels/" + f.channelID + "/messenger"
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	userID := "user-" + uuid.NewV7().String()
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix(), "name": "Ada", "email": "Ada@Example.com"}

	// 企业尚未生成密钥时签名身份一律失效。
	status, payload, _ := customerRequest(t, service, http.MethodGet, messengerPath, signCustomer(t, "unused", claims), "")
	require.Equal(t, http.StatusUnauthorized, status, "未生成密钥 payload=%+v", payload)
	require.Equal(t, appservice.WebsiteCustomerIdentityInvalidReason, payload["error"].(map[string]any)["reason"], "未生成密钥 payload=%+v", payload)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	token := signCustomer(t, secret, claims)

	// 错误密钥签发的身份被拒绝；有效身份初始化不签发匿名访客 Cookie。
	status, _, _ = customerRequest(t, service, http.MethodGet, messengerPath, signCustomer(t, "wrong-secret", claims), "")
	require.Equal(t, http.StatusUnauthorized, status, "错误密钥")
	status, messenger, header := customerRequest(t, service, http.MethodGet, messengerPath, token, "")
	require.Equal(t, http.StatusOK, status, "登录用户初始化 payload=%+v", messenger)
	require.Equal(t, "", messenger["visitorToken"], "登录用户初始化 payload=%+v", messenger)
	require.Empty(t, header.Get("Set-Cookie"), "登录用户初始化 cookie")

	// 首条消息建立带企业用户编号的联系人，访客上下文去掉查询串与 hash。
	send := func(channelMessagesPath, pageURL, referrer string) map[string]any {
		body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"我的订单到哪了","replyToMessageId":"","conversationId":null,` +
			`"page":{"url":"` + pageURL + `","title":"订单详情","referrer":"` + referrer + `","language":"ja-JP","timeZone":"Asia/Tokyo"}}`
		status, result, _ := customerRequest(t, service, http.MethodPost, channelMessagesPath, token, body)
		require.Equal(t, http.StatusOK, status, "登录用户发送 payload=%+v", result)
		return result
	}
	first := send(messagesPath, "https://shop.example.com/orders/42?token=secret#detail", "https://www.google.com/search?q=demo")
	conversationID := first["conversation"].(map[string]any)["id"].(string)
	contact := &servermodels.Contact{}
	require.NoError(t, f.db.NewSelect().Model(contact).Where("workspace_id = ? AND external_user_id = ?", f.owner.Workspace.ID, userID).Scan(ctx))
	identity := &servermodels.ChannelIdentity{}
	require.NoError(t, f.db.NewSelect().Model(identity).Where("workspace_id = ? AND channel_id = ?", f.owner.Workspace.ID, f.channelID).Where("external_id = ?", "web-user:"+userID).Scan(ctx))
	require.Equal(t, contact.ID, *identity.ContactID)
	require.NotNil(t, identity.DisplayName)
	require.Equal(t, "Ada", *identity.DisplayName)
	require.Nil(t, contact.DisplayName)
	require.Equal(t, string(domain.ContactStageVisitor), contact.Stage)
	profile, err := contactaction.NewGetCustomerProfileQuery(f.db).Execute(ctx, f.owner, conversationID)
	require.NoError(t, err)
	visit := profile.VisitorContext
	require.True(t, profile.IdentityVerified)
	require.Equal(t, userID, profile.ExternalUserID)
	require.Equal(t, "ada@example.com", profile.Email)
	require.NotNil(t, visit)
	require.NotEmpty(t, visit.Browser)
	if diff := cmp.Diff(domain.VisitorContext{
		PageURL: "https://shop.example.com/orders/42", ReferrerURL: "https://www.google.com/search",
		DeviceType: string(domain.VisitorDeviceDesktop), Language: "ja-JP", TimeZone: "Asia/Tokyo", Country: "JP",
	}, *visit, cmpopts.IgnoreFields(domain.VisitorContext{}, "PageTitle", "Browser", "OS")); diff != "" {
		t.Fatalf("访客上下文不符 (-want +got):\n%s", diff)
	}

	// 同一周期的后续消息更新当前页，来源页保留周期开始时的记录。
	body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"补充一下","replyToMessageId":"","conversationId":"` + conversationID + `",` +
		`"page":{"url":"https://shop.example.com/help","title":"帮助","referrer":"https://other.example.com/","language":"ja-JP","timeZone":"Asia/Tokyo"}}`
	status, result, _ := customerRequest(t, service, http.MethodPost, messagesPath, token, body)
	require.Equal(t, http.StatusOK, status, "同周期发送 payload=%+v", result)
	profile, err = contactaction.NewGetCustomerProfileQuery(f.db).Execute(ctx, f.owner, conversationID)
	require.NoError(t, err)
	require.Equal(t, "https://shop.example.com/help", profile.VisitorContext.PageURL)
	require.Equal(t, "https://www.google.com/search", profile.VisitorContext.ReferrerURL)

	// 同一企业用户编号在另一个网站渠道复用同一联系人，主邮箱不重复添加。
	other, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "第二个网站", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	send("/public/website-channels/"+other.ID+"/messages", "https://blog.example.com/", "")
	var identityContacts []string
	require.NoError(t, f.db.NewSelect().Table("channel_identities").Column("contact_id").Where("workspace_id = ? AND external_id = ?", f.owner.Workspace.ID, "web-user:"+userID).Scan(ctx, &identityContacts))
	emails, err := f.db.NewSelect().Table("contact_methods").Where("contact_id = ? AND type = ?", contact.ID, domain.ContactMethodTypeEmail).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{contact.ID, contact.ID}, identityContacts, "跨渠道联系人")
	require.Equal(t, int64(1), emails, "跨渠道联系人 emails")

	// 首条消息是附件时，创建上传即建立带企业用户编号的联系人。
	attachmentUserID := "user-" + uuid.NewV7().String()
	attachmentToken := signCustomer(t, secret, jwt.MapClaims{"sub": attachmentUserID, "exp": time.Now().Add(time.Hour).Unix(), "email": "grace@example.com"})
	upload := `{"fileName":"订单截图.png","contentType":"image/png","byteSize":128}`
	status, result, _ = customerRequest(t, service, http.MethodPost, "/public/website-channels/"+f.channelID+"/attachments", attachmentToken, upload)
	require.Equal(t, http.StatusOK, status, "登录用户创建上传 payload=%+v", result)
	require.Equal(t, attachmentToken, result["request"].(map[string]any)["headers"].(map[string]any)["X-Customer-Token"], "登录用户创建上传 payload=%+v", result)
	attachmentContact := &servermodels.Contact{}
	require.NoError(t, f.db.NewSelect().Model(attachmentContact).
		Join("JOIN channel_identities AS ci ON ci.contact_id = ct.id AND ci.workspace_id = ct.workspace_id").
		Where("ct.workspace_id = ?", f.owner.Workspace.ID).
		Where("ci.external_id = ?", "web-user:"+attachmentUserID).
		Scan(ctx))
	require.NotNil(t, attachmentContact.ExternalUserID, "附件先行的联系人")
	require.Equal(t, attachmentUserID, *attachmentContact.ExternalUserID, "附件先行的联系人")

	// 重新生成密钥后旧签名立即失效。
	secret, err = customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	directoryPath := "/public/website-channels/" + f.channelID + "/conversations"
	status, payload, _ = customerRequest(t, service, http.MethodGet, directoryPath, token, "")
	require.Equal(t, http.StatusUnauthorized, status, "旧密钥 payload=%+v", payload)
	require.Equal(t, appservice.WebsiteCustomerIdentityInvalidReason, payload["error"].(map[string]any)["reason"], "旧密钥 payload=%+v", payload)
	token = signCustomer(t, secret, claims)
	status, directory, _ := customerRequest(t, service, http.MethodGet, directoryPath, token, "")
	require.Equal(t, http.StatusOK, status, "新密钥目录 payload=%+v", directory)
	require.Len(t, directory["conversations"].([]any), 1, "新密钥目录 payload=%+v", directory)
}
