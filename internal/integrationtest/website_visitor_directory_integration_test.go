//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	"github.com/runforyou-ai/cervi/internal/api"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// websiteVisitorHTTP 通过公开路由调用网站访客接口。
type websiteVisitorHTTP struct {
	service *api.Service
}

// request 按访客 Token 与请求体执行一次公开请求，token 为空时不携带凭据。
func (h websiteVisitorHTTP) request(t *testing.T, method, path, token, body string, useCookie bool) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	switch {
	case token == "":
	case useCookie:
		// 渠道级 Cookie 名称由路径中的渠道编号决定。
		channelID := strings.Split(strings.TrimPrefix(path, "/public/website-channels/"), "/")[0]
		request.AddCookie(&http.Cookie{Name: "visitor_" + channelID, Value: token})
	default:
		request.Header.Set("X-Visitor-Token", token)
	}
	response := httptest.NewRecorder()
	h.service.ServeHTTP(response, request)
	payload := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析公开响应 %s: %v", response.Body.String(), err)
		}
	}
	return response.Code, payload
}

// visitorDirectoryIDs 取出目录响应中的线程编号，并核对每行只包含访客公开投影字段。
func visitorDirectoryIDs(t *testing.T, payload map[string]any) []string {
	t.Helper()
	rows, ok := payload["conversations"].([]any)
	if !ok {
		t.Fatalf("目录响应缺少线程集合: %+v", payload)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			t.Fatalf("目录行格式错误: %+v", row)
		}
		// 访客公开投影固定为下列字段，内部处理信息不外泄。
		want := []string{"id", "lastMessageAt", "lastMessageSeq", "preview", "serviceSession", "title"}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, want) {
			t.Fatalf("目录行字段 got=%v want=%v", keys, want)
		}
		session, ok := fields["serviceSession"].(map[string]any)
		reception, receptionOK := session["reception"].(map[string]any)
		receptionKeys := make([]string, 0, len(reception))
		for key := range reception {
			receptionKeys = append(receptionKeys, key)
		}
		slices.Sort(receptionKeys)
		if !ok || !receptionOK || len(session) != 3 || session["id"] == nil || session["status"] == nil ||
			!slices.Equal(receptionKeys, []string{"handlerAvatarUrl", "handlerName", "handlerType", "nextOpeningAt", "online", "reply"}) {
			t.Fatalf("处理周期投影错误: %+v", fields["serviceSession"])
		}
		ids = append(ids, fields["id"].(string))
	}
	slices.Sort(ids)
	return ids
}

// TestWebsiteVisitorDirectoryHTTP 验证公开目录接口的共同授权、跨身份与跨渠道隔离、未知线程发现，以及只读请求不建立联系人。
func TestWebsiteVisitorDirectoryHTTP(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	scheduler := agentrunaction.NewScheduler(newTestTasks(f.db))
	visitorService := appservice.NewWebsiteVisitorService(direct.NewWebsiteVisitorBackend(f.db, scheduler, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil))
	application := appservice.New(direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil))
	client := websiteVisitorHTTP{service: api.NewService(application, api.WithWebsiteVisitor(visitorService, false, ""))}
	directoryPath := "/public/website-channels/" + f.channelID + "/conversations"
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	// contacts 读取当前企业的联系人数量。
	contacts := func() int {
		count, err := f.db.NewSelect().Model((*servermodels.Contact)(nil)).Where("organization_id = ?", f.owner.Organization.ID).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}

	// 缺少访客 Token 与 Token 格式非法都被共同授权拒绝。
	for _, token := range []string{"", "not-a-visitor-token"} {
		if status, _ := client.request(t, http.MethodGet, directoryPath, token, "", false); status != http.StatusBadRequest {
			t.Fatalf("无效访客 Token status=%d", status)
		}
	}

	// 空白页初始化两次只签发 Token，不建立联系人。
	before := contacts()
	status, messenger := client.request(t, http.MethodGet, "/public/website-channels/"+f.channelID+"/messenger", "", "", false)
	token, _ := messenger["visitorToken"].(string)
	if status != http.StatusOK || token == "" {
		t.Fatalf("初始化 status=%d payload=%+v", status, messenger)
	}
	if status, _ = client.request(t, http.MethodGet, "/public/website-channels/"+f.channelID+"/messenger", token, "", true); status != http.StatusOK {
		t.Fatalf("重复初始化 status=%d", status)
	}
	if status, payload := client.request(t, http.MethodGet, directoryPath, token, "", true); status != http.StatusOK || len(visitorDirectoryIDs(t, payload)) != 0 {
		t.Fatalf("尚无线程的目录 status=%d payload=%+v", status, payload)
	}
	if after := contacts(); after != before {
		t.Fatalf("只读请求建立了联系人 before=%d after=%d", before, after)
	}

	// 同一访客的两个标签页各新建线程，目录互相发现。
	created := make([]string, 0, 2)
	for _, body := range []string{"标签页 A 的问题", "标签页 B 的问题"} {
		send := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"` + body + `","replyToMessageId":"","conversationId":null}`
		status, result := client.request(t, http.MethodPost, messagesPath, token, send, false)
		conversation, ok := result["conversation"].(map[string]any)
		if status != http.StatusOK || !ok || result["createdConversation"] != true {
			t.Fatalf("访客发送 status=%d payload=%+v", status, result)
		}
		created = append(created, conversation["id"].(string))
	}
	slices.Sort(created)
	status, payload := client.request(t, http.MethodGet, directoryPath, token, "", true)
	if status != http.StatusOK || !slices.Equal(visitorDirectoryIDs(t, payload), created) {
		t.Fatalf("两标签页目录 status=%d payload=%+v want=%v", status, payload, created)
	}

	// 另一访客身份读不到这些线程，伪造线程编号同样被拒绝。
	const otherToken = "fedcba9876543210fedcba9876543210"
	if status, payload := client.request(t, http.MethodGet, directoryPath, otherToken, "", false); status != http.StatusOK || len(visitorDirectoryIDs(t, payload)) != 0 {
		t.Fatalf("跨访客目录 status=%d payload=%+v", status, payload)
	}
	historyPath := "/public/website-channels/" + f.channelID + "/conversations/" + created[0] + "/messages"
	if status, _ := client.request(t, http.MethodGet, historyPath, otherToken, "", false); status != http.StatusNotFound {
		t.Fatalf("跨访客读取线程 status=%d", status)
	}

	// 另一个网站渠道下同一 Token 是不同身份，读不到原渠道线程。
	other, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "另一个网站渠道", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status, payload := client.request(t, http.MethodGet, "/public/website-channels/"+other.ID+"/conversations", token, "", false); status != http.StatusOK || len(visitorDirectoryIDs(t, payload)) != 0 {
		t.Fatalf("跨渠道目录 status=%d payload=%+v", status, payload)
	}
	if status, _ := client.request(t, http.MethodGet, "/public/website-channels/"+other.ID+"/conversations/"+created[0]+"/messages", token, "", false); status != http.StatusNotFound {
		t.Fatalf("跨渠道读取线程 status=%d", status)
	}

	// 目录只接受 GET。
	if status, _ := client.request(t, http.MethodPost, directoryPath, token, "{}", false); status != http.StatusMethodNotAllowed {
		t.Fatalf("目录 POST status=%d", status)
	}
}
