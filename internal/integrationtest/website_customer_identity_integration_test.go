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
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// customerRequest 以签名身份执行一次网站访客公开请求，返回状态、响应体与响应头。
func customerRequest(t *testing.T, service *api.Service, method, path, customerToken, body string) (int, map[string]any, http.Header) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Customer-Token", customerToken)
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	request.Header.Set("CF-IPCountry", "jp")
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	payload := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析公开响应 %s: %v", response.Body.String(), err)
		}
	}
	return response.Code, payload, response.Header()
}

// signCustomer 以企业客户身份密钥签发测试用签名身份。
func signCustomer(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestWebsiteCustomerIdentityHTTP 验证签名身份的验签与失效、登录用户联系人的关联与跨渠道复用（含附件先行）、签名邮箱补充，以及访客上下文按周期保存。
func TestWebsiteCustomerIdentityHTTP(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	scheduler := agentrunaction.NewScheduler(newTestTasks(f.db))
	visitorService := appservice.NewWebsiteVisitorService(direct.NewWebsiteVisitorBackend(f.db, scheduler, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil))
	application := appservice.New(direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil, nil))
	service := api.NewService(application, api.WithWebsiteVisitor(visitorService, false, "CF-IPCountry"))
	messengerPath := "/public/website-channels/" + f.channelID + "/messenger"
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	userID := "user-" + uuid.NewV7().String()
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix(), "name": "Ada", "email": "Ada@Example.com"}

	// 企业尚未生成密钥时签名身份一律失效。
	if status, payload, _ := customerRequest(t, service, http.MethodGet, messengerPath, signCustomer(t, "unused", claims), ""); status != http.StatusUnauthorized || payload["error"].(map[string]any)["reason"] != appservice.WebsiteCustomerIdentityInvalidReason {
		t.Fatalf("未生成密钥 status=%d payload=%+v", status, payload)
	}
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	token := signCustomer(t, secret, claims)

	// 错误密钥签发的身份被拒绝；有效身份初始化不签发匿名访客 Cookie。
	if status, _, _ := customerRequest(t, service, http.MethodGet, messengerPath, signCustomer(t, "wrong-secret", claims), ""); status != http.StatusUnauthorized {
		t.Fatalf("错误密钥 status=%d", status)
	}
	status, messenger, header := customerRequest(t, service, http.MethodGet, messengerPath, token, "")
	if status != http.StatusOK || messenger["visitorToken"] != "" || header.Get("Set-Cookie") != "" {
		t.Fatalf("登录用户初始化 status=%d payload=%+v cookie=%q", status, messenger, header.Get("Set-Cookie"))
	}

	// 首条消息建立带企业用户编号的联系人，访客上下文去掉查询串与 hash。
	send := func(channelMessagesPath, pageURL, referrer string) map[string]any {
		body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"我的订单到哪了","replyToMessageId":"","conversationId":null,` +
			`"page":{"url":"` + pageURL + `","title":"订单详情","referrer":"` + referrer + `","language":"ja-JP","timeZone":"Asia/Tokyo"}}`
		status, result, _ := customerRequest(t, service, http.MethodPost, channelMessagesPath, token, body)
		if status != http.StatusOK {
			t.Fatalf("登录用户发送 status=%d payload=%+v", status, result)
		}
		return result
	}
	first := send(messagesPath, "https://shop.example.com/orders/42?token=secret#detail", "https://www.google.com/search?q=demo")
	conversationID := first["conversation"].(map[string]any)["id"].(string)
	contact := &servermodels.Contact{}
	if err := f.db.NewSelect().Model(contact).Where("organization_id = ? AND external_user_id = ?", f.owner.Organization.ID, userID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	identity := &servermodels.ContactChannelIdentity{}
	if err := f.db.NewSelect().Model(identity).Where("organization_id = ? AND channel_id = ?", f.owner.Organization.ID, f.channelID).Where("external_id = ?", "web-user:"+userID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if identity.ContactID != contact.ID || identity.DisplayName == nil || *identity.DisplayName != "Ada" || contact.DisplayName != nil || contact.Stage != string(domain.ContactStageVisitor) {
		t.Fatalf("联系人 contact=%+v identity=%+v", contact, identity)
	}
	profile, err := contactaction.NewGetCustomerProfileQuery(f.db).Execute(ctx, f.owner, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	visit := profile.VisitorContext
	if !profile.IdentityVerified || profile.ExternalUserID != userID || profile.Email != "ada@example.com" || visit == nil ||
		visit.PageURL != "https://shop.example.com/orders/42" || visit.ReferrerURL != "https://www.google.com/search" ||
		visit.Browser == "" || visit.DeviceType != string(domain.VisitorDeviceDesktop) || visit.Language != "ja-JP" || visit.TimeZone != "Asia/Tokyo" || visit.Country != "JP" {
		t.Fatalf("客户资料 profile=%+v visit=%+v", profile, visit)
	}

	// 同一周期的后续消息更新当前页，来源页保留周期开始时的记录。
	body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"补充一下","replyToMessageId":"","conversationId":"` + conversationID + `",` +
		`"page":{"url":"https://shop.example.com/help","title":"帮助","referrer":"https://other.example.com/","language":"ja-JP","timeZone":"Asia/Tokyo"}}`
	if status, result, _ := customerRequest(t, service, http.MethodPost, messagesPath, token, body); status != http.StatusOK {
		t.Fatalf("同周期发送 status=%d payload=%+v", status, result)
	}
	profile, err = contactaction.NewGetCustomerProfileQuery(f.db).Execute(ctx, f.owner, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.VisitorContext.PageURL != "https://shop.example.com/help" || profile.VisitorContext.ReferrerURL != "https://www.google.com/search" {
		t.Fatalf("同周期访客上下文 %+v", profile.VisitorContext)
	}

	// 同一企业用户编号在另一个网站渠道复用同一联系人，主邮箱不重复添加。
	other, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "第二个网站", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	send("/public/website-channels/"+other.ID+"/messages", "https://blog.example.com/", "")
	var identityContacts []string
	if err := f.db.NewSelect().Table("contact_channel_identities").Column("contact_id").Where("organization_id = ? AND external_id = ?", f.owner.Organization.ID, "web-user:"+userID).Scan(ctx, &identityContacts); err != nil {
		t.Fatal(err)
	}
	emails, err := f.db.NewSelect().Table("contact_methods").Where("contact_id = ? AND type = ?", contact.ID, domain.ContactMethodTypeEmail).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(identityContacts) != 2 || identityContacts[0] != contact.ID || identityContacts[1] != contact.ID || emails != 1 {
		t.Fatalf("跨渠道联系人 identities=%v emails=%d", identityContacts, emails)
	}

	// 首条消息是附件时，创建上传即建立带企业用户编号的联系人。
	attachmentUserID := "user-" + uuid.NewV7().String()
	attachmentToken := signCustomer(t, secret, jwt.MapClaims{"sub": attachmentUserID, "exp": time.Now().Add(time.Hour).Unix(), "email": "grace@example.com"})
	upload := `{"fileName":"订单截图.png","contentType":"image/png","byteSize":128}`
	if status, result, _ := customerRequest(t, service, http.MethodPost, "/public/website-channels/"+f.channelID+"/attachments", attachmentToken, upload); status != http.StatusOK || result["request"].(map[string]any)["headers"].(map[string]any)["X-Customer-Token"] != attachmentToken {
		t.Fatalf("登录用户创建上传 status=%d payload=%+v", status, result)
	}
	attachmentContact := &servermodels.Contact{}
	if err := f.db.NewSelect().Model(attachmentContact).
		Join("JOIN contact_channel_identities AS cci ON cci.contact_id = ct.id AND cci.organization_id = ct.organization_id").
		Where("ct.organization_id = ?", f.owner.Organization.ID).
		Where("cci.external_id = ?", "web-user:"+attachmentUserID).
		Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if attachmentContact.ExternalUserID == nil || *attachmentContact.ExternalUserID != attachmentUserID {
		t.Fatalf("附件先行的联系人 %+v", attachmentContact)
	}

	// 重新生成密钥后旧签名立即失效。
	secret, err = customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	directoryPath := "/public/website-channels/" + f.channelID + "/conversations"
	if status, payload, _ := customerRequest(t, service, http.MethodGet, directoryPath, token, ""); status != http.StatusUnauthorized || payload["error"].(map[string]any)["reason"] != appservice.WebsiteCustomerIdentityInvalidReason {
		t.Fatalf("旧密钥 status=%d payload=%+v", status, payload)
	}
	token = signCustomer(t, secret, claims)
	status, directory, _ := customerRequest(t, service, http.MethodGet, directoryPath, token, "")
	if status != http.StatusOK || len(directory["conversations"].([]any)) != 1 {
		t.Fatalf("新密钥目录 status=%d payload=%+v", status, directory)
	}
}
