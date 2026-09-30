//go:build !server

package apiproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
)

// TestBackendInboxWindow 验证原生端窗口请求、原边界和独立锚点头像归一化。
func TestBackendInboxWindow(t *testing.T) {
	filter := appservice.InboxQuery{
		Scope: appservice.InboxScopeAll, AssigneeFilter: appservice.InboxAssigneeFilterIdentity, AssigneeIdentityID: "peer", Audience: appservice.ServiceAudienceCustomer,
		ChannelID: "channel", ServiceStatus: appservice.ServiceSessionStatusClosed, Kinds: []appservice.ConversationType{appservice.ConversationTypeChannel},
	}
	contextInput := appservice.InboxContextInput{Query: filter, AnchorID: "anchor", AnchorCursor: "old", BeforeLimit: 3, AfterLimit: 5}
	windowInput := appservice.InboxWindowInput{Query: filter, StartCursor: "start", EndCursor: "end"}
	row := appservice.InboxConversation{ID: "anchor", PositionCursor: "new", Direct: &appservice.DirectInboxConversation{PeerAvatarURL: "/storage/avatar.png"}}
	window := appservice.InboxWindow{Conversations: []appservice.InboxConversation{row}, StartCursor: "start", EndCursor: "end", HasBefore: true, HasAfter: true}
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer window-token" {
			t.Errorf("invalid request=%s %s", request.Method, request.URL)
		}
		switch request.URL.Path {
		case "/api/inbox/context/query":
			var input appservice.InboxContextInput
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || !reflect.DeepEqual(input, contextInput) {
				t.Errorf("context input=%+v err=%v", input, err)
			}
			_ = json.NewEncoder(writer).Encode(appservice.InboxContext{Anchor: appservice.InboxConversationResult{ID: "anchor", Availability: appservice.InboxConversationMatching, Conversation: &row}, Window: window})
		case "/api/inbox/window/query":
			var input appservice.InboxWindowInput
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || !reflect.DeepEqual(input, windowInput) {
				t.Errorf("window input=%+v err=%v", input, err)
			}
			_ = json.NewEncoder(writer).Encode(window)
		default:
			t.Errorf("unexpected path=%s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer remote.Close()
	backend, err := newTestBackend(&memoryStore{serverURL: remote.URL, credentialSet: true, credential: clientsession.Credential{ServerURL: remote.URL, Token: "window-token", ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	located, err := backend.GetInboxContext(context.Background(), appservice.RequestMeta{}, contextInput)
	if err != nil || located.Anchor.Conversation == nil || located.Anchor.Conversation.Direct.PeerAvatarURL != remote.URL+"/storage/avatar.png" || located.Anchor.Conversation.PositionCursor != "new" {
		t.Fatalf("context=%+v err=%v", located, err)
	}
	refreshed, err := backend.ReadInboxWindow(context.Background(), appservice.RequestMeta{}, windowInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []appservice.InboxWindow{located.Window, refreshed} {
		if result.StartCursor != "start" || result.EndCursor != "end" || !result.HasBefore || !result.HasAfter || len(result.Conversations) != 1 || result.Conversations[0].Direct.PeerAvatarURL != remote.URL+"/storage/avatar.png" || result.Conversations[0].PositionCursor != "new" {
			t.Fatalf("window=%+v", result)
		}
	}
}
