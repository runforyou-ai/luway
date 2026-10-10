//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// setWebsiteChannelSetting 直接改写网站渠道的一项访客聊天界面开关。
func setWebsiteChannelSetting(t *testing.T, db *bun.DB, channelID, column string, enabled bool) {
	t.Helper()
	_, err := db.NewUpdate().Model((*servermodels.WebsiteChannelSetting)(nil)).
		Set("? = ?", bun.Ident(column), enabled).
		Where("channel_id = ?", channelID).
		Exec(context.Background())
	require.NoError(t, err)
}

// TestWebsiteVisitorConversationFollowsMultipleConversationsSetting 验证未开启多会话时未指定会话的消息进入最近有消息的会话，开启后新建会话。
func TestWebsiteVisitorConversationFollowsMultipleConversationsSetting(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", false)
	send := func() customerchataction.ReceiveWebsiteCustomerMessageResult {
		t.Helper()
		result, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: websiteVisitorExternalID, ClientMessageID: uuid.NewV7().String(), Body: "再问一个问题",
		})
		require.NoError(t, err)
		return result
	}
	result := send()
	require.False(t, result.CreatedConversation, "single conversation result=%+v", result)
	require.Equal(t, f.conversationID, result.Conversation.ID, "single conversation result=%+v", result)
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", true)
	created := send()
	require.True(t, created.CreatedConversation, "multiple conversations result=%+v", created)
	require.NotEqual(t, f.conversationID, created.Conversation.ID, "multiple conversations result=%+v", created)
	// 再次关闭多会话后，未指定会话的消息进入最近有消息的会话。
	setWebsiteChannelSetting(t, f.db, f.channelID, "multiple_conversations_enabled", false)
	result = send()
	require.False(t, result.CreatedConversation, "recent conversation result=%+v", result)
	require.Equal(t, created.Conversation.ID, result.Conversation.ID, "recent conversation result=%+v", result)
}
