//go:build server

package realtime

import "testing"

func TestNotificationPersistent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind       Kind
		persistent bool
	}{
		{KindSessionLoggedOut, true},
		{KindUserDisabled, true},
		{KindWorkspaceStatusChanged, true},
		{KindMembershipAdded, true},
		{KindChannelDisabled, true},
		{KindCustomerIdentityRevoked, true},
		{KindComputerCredentialRevoked, true},
		{KindConversationRemoved, true},
		{KindUserNotification, true},
		{KindConversationChanged, false},
		{KindConversationStateChanged, false},
		{KindConversationTyping, false},
		{KindVisitorTyping, false},
		{KindPinOrderChanged, false},
		{KindComputerWork, false},
		{KindReceptionChanged, false},
		{KindKnowledgeGapsChanged, false},
		{KindServiceReportsChanged, false},
		{KindChannelChanged, false},
		{KindAgentMemoryChanged, false},
		{KindToolDecisionsChanged, false},
		{KindAgentRunEnded, false},
		{KindAgentInputAdded, false},
		{KindAgentToolCallSettled, false},
		{KindIdentityProfileChanged, false},
	}
	for _, tc := range cases {
		got := Notification{Kind: tc.kind}.Persistent()
		if got != tc.persistent {
			t.Fatalf("%s: Persistent()=%v, want %v", tc.kind, got, tc.persistent)
		}
	}
}
