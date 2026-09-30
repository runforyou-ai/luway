//go:build server

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

type inboxPageBackend struct {
	appservice.Backend
	input appservice.LoadInboxInput
}

// LoadInbox 记录适配后的分页输入并返回续页元数据。
func (b *inboxPageBackend) LoadInbox(_ context.Context, _ appservice.RequestMeta, input appservice.LoadInboxInput) (appservice.Inbox, error) {
	b.input = input
	return appservice.Inbox{Conversations: []appservice.InboxConversation{}, StartCursor: "first", EndCursor: "last", HasBefore: true, HasMore: true, NextCursor: "next-page", UnreadCount: 80, AttentionUnreadCount: 70}, nil
}

// TestInboxPaginationHTTP 验证 GET 参数、默认页大小、非法页大小与分页响应。
func TestInboxPaginationHTTP(t *testing.T) {
	backend := &inboxPageBackend{}
	server := httptest.NewServer(NewService(appservice.New(backend)))
	defer server.Close()
	for _, test := range []struct {
		query  string
		limit  int
		cursor string
		before string
	}{
		{"", 50, "", ""},
		{"?scope=all&assigneeFilter=identity&assigneeIdentityId=peer&channelId=channel&audience=customer&serviceStatus=closed&limit=7&cursor=original-boundary", 7, "original-boundary", ""},
		{"?scope=chat&kinds=group&kinds=direct&limit=9", 9, "", ""},
		{"?scope=pending&pendingKind=queue&queueFilter=public&beforeCursor=previous-boundary&limit=8", 8, "", "previous-boundary"},
	} {
		response := doJSON(t, http.MethodGet, server.URL+"/inbox"+test.query, nil, "token")
		var page appservice.Inbox
		err := json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil || backend.input.Limit != test.limit || backend.input.Cursor != test.cursor || backend.input.BeforeCursor != test.before || page.StartCursor != "first" || page.EndCursor != "last" || !page.HasBefore || !page.HasMore || page.NextCursor != "next-page" || page.UnreadCount != 80 || page.AttentionUnreadCount != 70 {
			t.Fatalf("input=%+v response=%+v err=%v", backend.input, page, err)
		}
		if test.cursor != "" && (backend.input.Scope != appservice.InboxScopeAll || backend.input.AssigneeFilter != appservice.InboxAssigneeFilterIdentity ||
			backend.input.AssigneeIdentityID != "peer" || backend.input.ChannelID != "channel" || backend.input.Audience != appservice.ServiceAudienceCustomer || backend.input.ServiceStatus != appservice.ServiceSessionStatusClosed) {
			t.Fatalf("filters lost=%+v", backend.input)
		}
		if test.limit == 8 && (backend.input.Scope != appservice.InboxScopePending || backend.input.PendingKind != appservice.InboxPendingKindQueue || backend.input.QueueFilter != appservice.ServiceQueueFilterPublic) {
			t.Fatalf("pending filters lost=%+v", backend.input)
		}
		if test.limit == 9 && !slices.Equal(backend.input.Kinds, []appservice.ConversationType{appservice.ConversationTypeGroup, appservice.ConversationTypeDirect}) {
			t.Fatalf("kinds lost=%+v", backend.input)
		}
	}
	for _, value := range []string{"0", "-1", "abc"} {
		response := doJSON(t, http.MethodGet, server.URL+"/inbox?limit="+value, nil, "token")
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid limit %s status=%d", value, response.StatusCode)
		}
	}
}
