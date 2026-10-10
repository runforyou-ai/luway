//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"uuid"

	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/searchtext"
)

// TestInboxSearch 验证消息拼音与编号检索、附件文件名、群名与成员、列表范围、会话范围和退群后的阅读边界。
func TestInboxSearch(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	trip := f.send(t, f.owner, "明天去长春出差，型号E-731需要带上", false)
	invoice := f.send(t, f.member, "报销发票已经提交", false)
	file, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: "季度合同.pdf", ContentType: "application/pdf", ByteSize: 10,
	})
	require.NoError(t, err)
	_, err = markFileUploaded(ctx, f.db, f.owner, file.ID, "")
	require.NoError(t, err)
	attachment, err := directchataction.NewSendAttachmentMessageAction(f.db, testEnqueuer, nil).Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		ConversationID: f.groupID, FileID: file.ID, ClientMessageID: uuid.NewV7().String(),
	})
	require.NoError(t, err)

	search := func(identity *servermodels.Identity, input inboxaction.SearchInput) inboxaction.SearchResult {
		t.Helper()
		result, err := query.Search(ctx, identity, input)
		require.NoError(t, err, "Search(%+v)", input)
		return result
	}
	messageIDs := func(result inboxaction.SearchResult) []string {
		return arr.Map(result.Messages, func(message inboxaction.SearchMessage) string { return message.ID })
	}

	cases := []struct {
		text, messageID, highlight string
	}{
		{text: "changchun", messageID: trip.ID, highlight: "长春"},
		{text: "e731", messageID: trip.ID, highlight: "E-731"},
		{text: "发票", messageID: invoice.ID, highlight: "发票"},
		{text: "hetong", messageID: attachment.Message.ID, highlight: "合同"},
	}
	for _, item := range cases {
		result := search(f.owner, inboxaction.SearchInput{Text: item.text, Range: inboxaction.SearchRangeReadable})
		index := slices.IndexFunc(result.Messages, func(message inboxaction.SearchMessage) bool { return message.ID == item.messageID })
		require.GreaterOrEqual(t, index, 0, "检索 %q 未命中 %s：%v", item.text, item.messageID, messageIDs(result))
		message := result.Messages[index]
		require.Equal(t, f.groupID, message.Conversation.ID, "检索 %q 的会话不正确", item.text)
		require.True(t, slices.ContainsFunc(message.Excerpt, func(segment searchtext.Segment) bool {
			return segment.Match && segment.Text == item.highlight
		}), "检索 %q 的高亮不正确：%+v", item.text, message)
	}

	names := search(f.owner, inboxaction.SearchInput{Text: "导航测试", Range: inboxaction.SearchRangeReadable})
	require.True(t, slices.ContainsFunc(names.Conversations, func(summary inboxaction.ConversationSummary) bool { return summary.ID == f.groupID }), "群名检索未命中：%+v", names.Conversations)
	people := search(f.owner, inboxaction.SearchInput{Text: "成员", Range: inboxaction.SearchRangeReadable})
	require.True(t, slices.ContainsFunc(people.People, func(person inboxaction.SearchPerson) bool {
		return person.Kind == inboxaction.SearchPersonMember && person.ID == f.member.WorkspaceIdentity.ID &&
			person.UserID != nil && *person.UserID == f.member.User.ID && person.AgentID == nil
	}), "成员检索未命中：%+v", people.People)

	provider := &servermodels.AIProvider{
		WorkspaceID: &f.owner.Workspace.ID, Brand: string(domain.AIProviderBrandOpenAI),
		Name: "检索测试模型服务", CredentialType: string(domain.AIProviderCredentialTypeAPIKey),
		APIKey: "test-key", APIURL: "https://example.com/v1",
	}
	_, err = f.db.NewInsert().Model(provider).
		Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx)
	require.NoError(t, err)
	model := &testAIModel{
		ProviderID: provider.ID, Identifier: "chat-model", Name: "检索测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	insertAIModels(t, f.db, model)
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "检索个人 AI 员工",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: model.ID, SystemInstruction: "负责检索测试。",
		}},
	})
	require.NoError(t, err)
	agents := search(f.owner, inboxaction.SearchInput{Text: "检索个人 AI 员工", Range: inboxaction.SearchRangeReadable})
	require.True(t, slices.ContainsFunc(agents.People, func(person inboxaction.SearchPerson) bool {
		return person.Kind == inboxaction.SearchPersonMember && person.IdentityType == domain.WorkspaceIdentityTypeAgent &&
			person.AgentID != nil && *person.AgentID == agent.ID && person.UserID == nil
	}), "AI 员工检索未命中：%+v", agents.People)

	customerList := search(f.owner, inboxaction.SearchInput{Text: "changchun", Range: inboxaction.SearchRangeList, List: inboxaction.LoadInput{Scope: domain.InboxScopeAll}})
	internalList := search(f.owner, inboxaction.SearchInput{Text: "changchun", Range: inboxaction.SearchRangeList, List: inboxaction.LoadInput{Scope: domain.InboxScopeChat}})
	require.Empty(t, customerList.Messages, "列表范围不正确")
	require.Contains(t, messageIDs(internalList), trip.ID, "列表范围不正确")

	inConversation := search(f.member, inboxaction.SearchInput{Text: "发票", Range: inboxaction.SearchRangeConversation, ConversationID: f.groupID})
	require.Equal(t, []string{invoice.ID}, messageIDs(inConversation), "会话范围结果不正确")
	require.Empty(t, inConversation.Conversations, "会话范围结果不正确")
	require.Empty(t, inConversation.People, "会话范围结果不正确")
	_, err = query.Search(ctx, f.owner, inboxaction.SearchInput{Text: "发票", Range: inboxaction.SearchRangeConversation, ConversationID: uuid.NewV7().String()})
	require.ErrorIs(t, err, inboxaction.ErrConversationUnavailable, "不可读会话")

	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, f.groupID))
	require.Empty(t, search(f.member, inboxaction.SearchInput{Text: "changchun", Range: inboxaction.SearchRangeReadable}).Messages, "退群成员仍能检索群消息")
	_, err = query.Search(ctx, f.member, inboxaction.SearchInput{Text: "发票", Range: inboxaction.SearchRangeConversation, ConversationID: f.groupID})
	require.ErrorIs(t, err, inboxaction.ErrConversationUnavailable, "退群成员会话范围")
}
