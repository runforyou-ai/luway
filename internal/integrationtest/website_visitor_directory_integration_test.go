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

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/mapx"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "解析公开响应 %s", response.Body.String())
	}
	return response.Code, payload
}

// visitorDirectoryIDs 取出目录响应中的线程编号，并核对每行只包含访客公开投影字段。
func visitorDirectoryIDs(t *testing.T, payload map[string]any) []string {
	t.Helper()
	rows, ok := payload["conversations"].([]any)
	require.True(t, ok, "目录响应缺少线程集合: %+v", payload)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		require.True(t, ok, "目录行格式错误: %+v", row)
		// 访客公开投影固定为下列字段，内部处理信息不外泄。
		want := []string{"id", "lastMessageAt", "lastMessageSeq", "preview", "serviceSession", "title"}
		require.Equal(t, want, mapx.SortedKeys(fields), "目录行字段")
		session, ok := fields["serviceSession"].(map[string]any)
		reception, receptionOK := session["reception"].(map[string]any)
		receptionKeys := mapx.SortedKeys(reception)
		require.True(t, ok, "处理周期投影错误: %+v", fields["serviceSession"])
		require.True(t, receptionOK, "处理周期投影错误: %+v", fields["serviceSession"])
		require.Len(t, session, 3, "处理周期投影错误: %+v", fields["serviceSession"])
		require.NotNil(t, session["id"], "处理周期投影错误: %+v", fields["serviceSession"])
		require.NotNil(t, session["status"], "处理周期投影错误: %+v", fields["serviceSession"])
		require.Equal(t, []string{"handlerAvatarUrl", "handlerName", "handlerType", "nextOpeningAt", "online", "reply"}, receptionKeys, "处理周期投影错误: %+v", fields["serviceSession"])
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
	scheduler := agentrunaction.NewScheduler(testEnqueuer)
	visitorService := direct.NewWebsiteVisitorBackend(f.db, scheduler, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	application := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	client := websiteVisitorHTTP{service: api.NewService(application, api.WithWebsiteVisitor(visitorService, func() bool { return false }))}
	directoryPath := "/public/website-channels/" + f.channelID + "/conversations"
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	// contacts 读取当前企业的联系人数量。
	contacts := func() int {
		count, err := f.db.NewSelect().Model((*servermodels.Contact)(nil)).Where("workspace_id = ?", f.owner.Workspace.ID).Count(ctx)
		require.NoError(t, err)
		return int(count)
	}

	// 缺少访客 Token 与 Token 格式非法都被共同授权拒绝。
	for _, token := range []string{"", "not-a-visitor-token"} {
		status, _ := client.request(t, http.MethodGet, directoryPath, token, "", false)
		require.Equal(t, http.StatusBadRequest, status, "无效访客 Token %q", token)
	}

	// 空白页初始化两次只签发 Token，不建立联系人。
	before := contacts()
	status, messenger := client.request(t, http.MethodGet, "/public/website-channels/"+f.channelID+"/messenger", "", "", false)
	token, _ := messenger["visitorToken"].(string)
	require.Equal(t, http.StatusOK, status, "初始化 payload=%+v", messenger)
	require.NotEmpty(t, token, "初始化 payload=%+v", messenger)
	status, _ = client.request(t, http.MethodGet, "/public/website-channels/"+f.channelID+"/messenger", token, "", true)
	require.Equal(t, http.StatusOK, status, "重复初始化")
	status, payload := client.request(t, http.MethodGet, directoryPath, token, "", true)
	require.Equal(t, http.StatusOK, status, "尚无线程的目录 payload=%+v", payload)
	require.Empty(t, visitorDirectoryIDs(t, payload), "尚无线程的目录 payload=%+v", payload)
	require.Equal(t, before, contacts(), "只读请求建立了联系人")

	// 同一访客的两个标签页各新建线程，目录互相发现。
	created := make([]string, 0, 2)
	for _, body := range []string{"标签页 A 的问题", "标签页 B 的问题"} {
		send := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"` + body + `","replyToMessageId":"","conversationId":null}`
		status, result := client.request(t, http.MethodPost, messagesPath, token, send, false)
		conversation, ok := result["conversation"].(map[string]any)
		require.Equal(t, http.StatusOK, status, "访客发送 payload=%+v", result)
		require.True(t, ok, "访客发送 payload=%+v", result)
		require.Equal(t, true, result["createdConversation"], "访客发送 payload=%+v", result)
		created = append(created, conversation["id"].(string))
	}
	slices.Sort(created)
	status, payload = client.request(t, http.MethodGet, directoryPath, token, "", true)
	require.Equal(t, http.StatusOK, status, "两标签页目录 payload=%+v", payload)
	require.Equal(t, created, visitorDirectoryIDs(t, payload), "两标签页目录 payload=%+v", payload)

	// 另一访客身份读不到这些线程，伪造线程编号同样被拒绝。
	const otherToken = "fedcba9876543210fedcba9876543210"
	status, payload = client.request(t, http.MethodGet, directoryPath, otherToken, "", false)
	require.Equal(t, http.StatusOK, status, "跨访客目录 payload=%+v", payload)
	require.Empty(t, visitorDirectoryIDs(t, payload), "跨访客目录 payload=%+v", payload)
	historyPath := "/public/website-channels/" + f.channelID + "/conversations/" + created[0] + "/messages"
	status, _ = client.request(t, http.MethodGet, historyPath, otherToken, "", false)
	require.Equal(t, http.StatusNotFound, status, "跨访客读取线程")

	// 另一个网站渠道下同一 Token 是不同身份，读不到原渠道线程。
	other, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "另一个网站渠道", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	status, payload = client.request(t, http.MethodGet, "/public/website-channels/"+other.ID+"/conversations", token, "", false)
	require.Equal(t, http.StatusOK, status, "跨渠道目录 payload=%+v", payload)
	require.Empty(t, visitorDirectoryIDs(t, payload), "跨渠道目录 payload=%+v", payload)
	status, _ = client.request(t, http.MethodGet, "/public/website-channels/"+other.ID+"/conversations/"+created[0]+"/messages", token, "", false)
	require.Equal(t, http.StatusNotFound, status, "跨渠道读取线程")

	// 目录只接受 GET。
	status, _ = client.request(t, http.MethodPost, directoryPath, token, "{}", false)
	require.Equal(t, http.StatusMethodNotAllowed, status, "目录 POST")
}
