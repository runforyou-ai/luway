//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// customerInboxFilter 描述一组客户会话筛选及其固定名称。
type customerInboxFilter struct {
	name  string
	input inboxaction.LoadInput
}

// customerInboxFilters 返回覆盖负责人与服务状态组合的服务会话筛选，mine 为 owner 负责，coworkers 为 member 负责。
func customerInboxFilters(ownerID, memberID string) []customerInboxFilter {
	return []customerInboxFilter{
		{"queue", inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned}},
		{"mine", inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: ownerID}},
		{"coworkers", inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: memberID}},
		{"closed", inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: ownerID, ServiceStatus: domain.ServiceSessionStatusClosed}},
	}
}

// inboxPaginationFixture 是收件箱分页测试的会话集合。
type inboxPaginationFixture struct {
	customerReadFixture
	internalIDs []string
	customerIDs map[string][]string
}

// newInboxPaginationFixture 用真实创建和发送入口建立四类各六十条会话。
func newInboxPaginationFixture(t *testing.T) inboxPaginationFixture {
	t.Helper()
	f := inboxPaginationFixture{customerReadFixture: newCustomerReadFixture(t), customerIDs: make(map[string][]string)}
	ctx := context.Background()
	provider := &servermodels.AIProvider{WorkspaceID: &f.owner.Workspace.ID, Brand: "openai", Name: "分页模型", CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "test", APIURL: "https://example.com/v1"}
	_, err := f.db.NewInsert().Model(provider).Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx)
	require.NoError(t, err)
	model := &testAIModel{ProviderID: provider.ID, Identifier: "test-model", Name: "分页模型", Type: "chat", InputModalities: json.RawMessage(`["text"]`), ContextWindow: 1000, MaxOutputTokens: 100}
	insertAIModels(t, f.db, model)
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "分页助手", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: model.ID, SystemInstruction: "测试分页"}}})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	startAgent := directchataction.NewSendFirstAgentTextMessageAction(f.db, testEnqueuer, agentrunaction.NewScheduler(tasks))
	buckets := []string{"queue", "mine", "coworkers", "closed"}
	for index := range 60 {
		peer, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: fmt.Sprintf("分页成员 %d", index), Email: servertest.UniqueEmail(fmt.Sprintf("page%d", index)), Password: "password123", RoleID: f.member.User.RoleID})
		require.NoError(t, err)
		direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: peer.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "单聊"})
		require.NoError(t, err)
		ai, err := startAgent.Execute(ctx, f.owner, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "AI 会话"})
		require.NoError(t, err)
		groupID := f.groupID
		customerID := f.conversationID
		if index > 0 {
			group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: fmt.Sprintf("分页群 %d", index), MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
			require.NoError(t, err)
			groupID = group.ID
			customer, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "访客会话"})
			require.NoError(t, err)
			customerID = customer.Conversation.ID
		}
		bucket := buckets[index%len(buckets)]
		if bucket != "queue" {
			assignee := f.owner
			if bucket == "coworkers" {
				assignee = f.member
			}
			_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, assignee, customerID)
			require.NoError(t, err)
			if bucket == "closed" {
				_, err := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, assignee, customerID)
				require.NoError(t, err)
			}
		}
		f.internalIDs = append(f.internalIDs, direct.Conversation.ID, ai.Conversation.ID, groupID)
		f.customerIDs[bucket] = append(f.customerIDs[bucket], customerID)
		// 每批四种类型共享微秒时间，定期插入空时间以覆盖完整空值分区。
		var activity *time.Time
		if index%7 != 0 {
			value := time.Date(2026, 9, 9, 0, 0, 0, 123000000, time.UTC).Add(time.Duration(index/2) * time.Microsecond)
			activity = &value
		}
		_, err = f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", activity).Where("id IN (?)", bun.List([]string{direct.Conversation.ID, ai.Conversation.ID, groupID, customerID})).Exec(ctx)
		require.NoError(t, err)
	}
	// 批量造数后刷新收件箱相关表的统计信息。
	analyzeInboxTables(ctx, t, f.db)
	return f
}

// analyzeInboxTables 按固定顺序刷新收件箱查询相关表的统计信息。
func analyzeInboxTables(ctx context.Context, t *testing.T, db *bun.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, "ANALYZE workspace_identities, chat_subjects, conversations, conversation_participants, conversation_user_states, messages")
	require.NoError(t, err)
}

// TestInboxPagination 验证全量 SQL 顺序、各筛选、重复请求和页外未读总数。
func TestInboxPagination(t *testing.T) {
	t.Parallel()
	f := newInboxPaginationFixture(t)
	ctx := context.Background()
	f.send(t, f.member, "页外未读", false)
	// 将未读会话放在最后，确保首屏总数并非来自首屏行的加总。
	_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = NULL").Where("id = ?", f.groupID).Exec(ctx)
	require.NoError(t, err)
	cases := []struct {
		name  string
		input inboxaction.LoadInput
		ids   []string
	}{
		{"service", inboxaction.LoadInput{Scope: domain.InboxScopeAll}, append(append(slices.Clone(f.customerIDs["queue"]), f.customerIDs["mine"]...), f.customerIDs["coworkers"]...)},
		{"chat", inboxaction.LoadInput{Scope: domain.InboxScopeChat, Limit: 17}, f.internalIDs},
	}
	for _, filter := range customerInboxFilters(f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID) {
		filter.input.Limit = 4
		cases = append(cases, struct {
			name  string
			input inboxaction.LoadInput
			ids   []string
		}{filter.name, filter.input, f.customerIDs[filter.name]})
	}
	cases = append(cases, struct {
		name  string
		input inboxaction.LoadInput
		ids   []string
	}{"unavailable_assignee", inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: uuid.NewV7().String()}, nil})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var expected []string
			require.NoError(t, f.db.NewSelect().Table("conversations").Column("id").Where("id IN (?)", bun.List(tc.ids)).OrderExpr("last_activity_at DESC NULLS LAST, id DESC").Scan(ctx, &expected))
			query := inboxaction.NewLoadInboxQuery(f.db)
			input := tc.input
			var actual []string
			for attempts := 0; ; attempts++ {
				require.LessOrEqual(t, attempts, len(expected), "pagination did not terminate")
				page, counts, err := query.Execute(ctx, f.owner, input)
				require.NoError(t, err)
				require.Equal(t, 1, counts.Unread, "page counts=%+v", counts)
				require.Equal(t, 1, counts.Attention, "page counts=%+v", counts)
				if attempts == 0 {
					repeated, _, err := query.Execute(ctx, f.owner, input)
					require.NoError(t, err)
					require.Equal(t, page.NextCursor, repeated.NextCursor)
					require.Equal(t, conversationIDs(page.Conversations), conversationIDs(repeated.Conversations))
					if input.Limit == 0 {
						require.Len(t, page.Conversations, min(50, len(expected)), "default page size")
					}
				}
				for _, row := range page.Conversations {
					actual = append(actual, row.ID)
				}
				if !page.HasMore {
					require.Empty(t, page.NextCursor, "terminal page returned cursor")
					break
				}
				require.NotEmpty(t, page.NextCursor, "cursor did not advance")
				require.NotEqual(t, input.Cursor, page.NextCursor, "cursor did not advance")
				input.Cursor = page.NextCursor
			}
			require.True(t, slices.Equal(actual, expected), "pages=%v expected=%v", actual, expected)
		})
	}
}

// TestInboxPaginationBoundaries 验证游标行删除、失权、空尾页及应用服务错误语义。
func TestInboxPaginationBoundaries(t *testing.T) {
	t.Parallel()
	for _, withTime := range []bool{false, true} {
		t.Run(fmt.Sprintf("activity=%t", withTime), func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx := context.Background()
			ids := []string{f.groupID}
			for range 3 {
				group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "边界群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
				require.NoError(t, err)
				ids = append(ids, group.ID)
			}
			if withTime {
				for index, id := range ids {
					activity := time.Date(2026, 9, 9, 0, 0, 0, 123456000, time.UTC).Add(time.Duration(index/2) * time.Microsecond)
					_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", activity).Where("id = ?", id).Exec(ctx)
					require.NoError(t, err)
				}
			}

			query := inboxaction.NewLoadInboxQuery(f.db)
			input := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Limit: 2}
			page, _, err := query.Execute(ctx, f.member, input)
			require.NoError(t, err)
			require.True(t, page.HasMore, "first page=%+v", page)
			input.Cursor = page.NextCursor
			// 核验时间与空时间边界在会话删除后仍保留原始值。
			_, err = f.db.NewDelete().Model((*servermodels.Conversation)(nil)).Where("id = ?", page.Conversations[1].ID).Exec(ctx)
			require.NoError(t, err)
			next, _, err := query.Execute(ctx, f.member, input)
			require.NoError(t, err)
			require.Len(t, next.Conversations, 2)
			require.False(t, next.HasMore)
			require.Equal(t, f.groupID, next.Conversations[1].ID)
			for _, row := range next.Conversations {
				_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: row.ID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
				require.NoError(t, err)
			}
			empty, _, err := query.Execute(ctx, f.member, input)
			require.NoError(t, err)
			require.NotNil(t, empty.Conversations)
			require.Empty(t, empty.Conversations)
			require.False(t, empty.HasMore)
			require.Empty(t, empty.NextCursor)
			login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.member.Account.Email, "password123")
			backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
			meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}
			for _, request := range []appservice.LoadInboxInput{
				{Scope: domain.InboxScopePending, Cursor: input.Cursor},
				{Scope: domain.InboxScopeChat, Cursor: "invalid"},
			} {
				_, err := backend.LoadInbox(ctx, meta, request)
				var apiError *appservice.Error
				require.ErrorAs(t, err, &apiError)
				require.Equal(t, "inbox_cursor_invalid", apiError.Reason)
			}
			_, _, err = query.Execute(ctx, f.owner, input)
			require.ErrorIs(t, err, inboxaction.ErrCursorInvalid, "other user cursor")
			foreign := newNavigationFixture(t)
			_, _, err = query.Execute(ctx, foreign.owner, input)
			require.ErrorIs(t, err, inboxaction.ErrCursorInvalid, "other workspace cursor")
		})
	}
}
