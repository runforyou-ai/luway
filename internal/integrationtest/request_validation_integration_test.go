//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestDeclarativeRequestValidation 验证分发层校验输入：路径参数不符合路由规则时按资源不存在返回，请求体字段按 validate 标签返回字段文案，通过校验的请求进入业务实现。
func TestDeclarativeRequestValidation(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "请求校验", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"})
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	memberID := uuid.NewV7().String()
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	err = backend.UpdateConversationArchive(ctx, meta, "not-a-uuid", appservice.ConversationArchiveInput{Archived: true})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.MarkConversationRead(ctx, meta, "not-a-uuid", appservice.MarkConversationReadInput{LastReadMessageID: "bad"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.MarkConversationRead(ctx, meta, uuid.NewV7().String(), appservice.MarkConversationReadInput{LastReadMessageID: "bad"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.RequireFieldError(t, err, "lastReadMessageId", i18n.FieldClientMessageIDInvalid)

	err = backend.UpdateConversationUnreadMark(ctx, meta, uuid.NewV7().String(), appservice.ConversationUnreadMarkInput{MarkedUnread: true})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.SendServiceTextMessage(ctx, meta, uuid.NewV7().String(), appservice.ServiceTextMessageInput{
		ClientMessageID: "bad", Body: "  ", Visibility: "secret", MentionIdentityIDs: []string{memberID, memberID},
	})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.RequireFieldError(t, err, "clientMessageId", i18n.FieldClientMessageIDInvalid)
	servertest.RequireFieldError(t, err, "body", i18n.FieldMessageBodyRequired)
	servertest.RequireFieldError(t, err, "visibility", i18n.FieldMessageVisibilityInvalid)
	servertest.RequireFieldError(t, err, "mentionIdentityIds", i18n.FieldMentionIdentityIDsInvalid)

	_, err = backend.SendServiceAttachmentMessage(ctx, meta, uuid.NewV7().String(), appservice.ServiceAttachmentMessageInput{
		ClientMessageID: uuid.NewV7().String(), FileID: uuid.NewV7().String(), Body: strings.Repeat("鹿", 4001),
	})
	servertest.RequireFieldError(t, err, "body", i18n.FieldMessageBodyTooLong)

	_, err = backend.CreateGroupConversation(ctx, meta, appservice.GroupConversationInput{Title: strings.Repeat("群", 101)})
	servertest.RequireFieldError(t, err, "title", i18n.FieldGroupTitleTooLong)
	servertest.RequireFieldError(t, err, "memberIdentityIds", i18n.FieldGroupMembersRequired)

	_, err = backend.CreateGroupConversation(ctx, meta, appservice.GroupConversationInput{MemberIdentityIDs: []string{memberID, "bad"}})
	servertest.RequireFieldError(t, err, "memberIdentityIds[1]", i18n.FieldGroupMemberIDsInvalid)
}

// TestCustomerServiceRequestValidation 验证渠道、联系人与客服设置接口在分发层按契约校验路径参数与请求字段，数组元素的错误按元素路径返回。
func TestCustomerServiceRequestValidation(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "客服请求校验", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"})
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	missingID := uuid.NewV7().String()

	_, err = backend.GetMessageChannel(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.UpdateContactField(ctx, meta, "not-a-uuid", appservice.ContactFieldInput{Name: "等级", Type: domain.ContactFieldTypeText})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	err = backend.ResolveChannelMessageDelivery(ctx, meta, missingID, "not-a-uuid", appservice.ChannelDeliveryResolveInput{})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.CreateMessageChannel(ctx, meta, appservice.CreateMessageChannelInput{Type: "fax", MessageChannelInput: appservice.MessageChannelInput{Name: " ", DefaultLocale: "fr-FR"}})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.RequireFieldError(t, err, "type", i18n.FieldChannelTypeInvalid)
	servertest.RequireFieldError(t, err, "name", i18n.FieldChannelNameRequired)
	servertest.RequireFieldError(t, err, "defaultLocale", i18n.FieldChannelDefaultLocaleInvalid)

	_, err = backend.UpdateWebsiteChannelChatInterface(ctx, meta, missingID, appservice.WebsiteChannelChatInterfaceInput{Title: "在线咨询", ThemeColor: "#FFF"})
	servertest.RequireFieldError(t, err, "themeColor", i18n.FieldChannelThemeColorInvalid)

	_, err = backend.UpdateWebsiteChannelHome(ctx, meta, missingID, appservice.WebsiteChannelHomeInput{
		Welcome: strings.Repeat("长", 101),
		Links:   []appservice.WebsiteChannelHomeLink{{Title: "文档", URL: "https://docs.example.com"}, {Title: "脚本", URL: "javascript:alert(1)"}},
	})
	servertest.RequireFieldError(t, err, "welcome", i18n.FieldChannelHomeGreetingTooLong)
	servertest.RequireFieldError(t, err, "links[1].url", i18n.FieldChannelHomeLinkURLInvalid)

	err = backend.BindChannelAccount(ctx, meta, missingID, missingID, appservice.ChannelAccountBindingInput{MemberIdentityID: "bad"})
	servertest.RequireFieldError(t, err, "memberIdentityId", i18n.ErrorChannelAccountMemberInvalid)

	_, err = backend.SaveWechatKeyChannelConnection(ctx, meta, missingID, appservice.WechatKeyConnectionInput{AppID: "wx0123456789abcdef", AppSecret: "secret", Token: "ab", EncryptionMode: "rot13"})
	servertest.RequireFieldError(t, err, "token", i18n.FieldWechatTokenInvalid)
	servertest.RequireFieldError(t, err, "encryptionMode", i18n.FieldWechatEncryptionModeInvalid)

	_, err = backend.CreateContact(ctx, meta, appservice.ContactInput{DisplayName: "访客", ChannelID: "bad", Stage: "partner"})
	servertest.RequireFieldError(t, err, "channelId", i18n.FieldContactChannelInvalid)
	servertest.RequireFieldError(t, err, "stage", i18n.FieldContactStageInvalid)

	_, err = backend.ListContacts(ctx, meta, appservice.ContactListInput{Sort: "name", PageSize: 101})
	servertest.RequireFieldError(t, err, "sort", i18n.FieldContactQueryInvalid)
	servertest.RequireFieldError(t, err, "pageSize", i18n.FieldContactQueryInvalid)

	_, err = backend.CreateServiceCategory(ctx, meta, appservice.ServiceCategoryInput{Name: "", TeamID: new("bad")})
	servertest.RequireFieldError(t, err, "name", i18n.FieldServiceCategoryNameRequired)
	servertest.RequireFieldError(t, err, "teamId", i18n.FieldTeamInvalid)

	_, err = backend.UpdateBusinessHours(ctx, meta, appservice.BusinessHours{TimeZone: "Asia/Shanghai"})
	servertest.RequireFieldError(t, err, "weekly", i18n.FieldBusinessHoursWeeklyInvalid)

	// 时区名区分大小写，与服务器文件系统无关。
	_, err = backend.UpdateBusinessHours(ctx, meta, appservice.BusinessHours{TimeZone: "asia/shanghai", Weekly: make([][]appservice.BusinessHoursPeriod, 7)})
	servertest.RequireFieldError(t, err, "timeZone", i18n.FieldTimeZoneInvalid)

	_, err = backend.UpdateServiceTimeouts(ctx, meta, appservice.ServiceTimeouts{ResponseReminderMinutes: 5, ResponseReclaimMinutes: 10, QueueReminderMinutes: 0, AIFollowUpMinutes: 1, AICloseMinutes: 1})
	servertest.RequireFieldError(t, err, "queueReminderMinutes", i18n.FieldServiceTimeoutMinutesInvalid)
}

// TestAIKnowledgeRequestValidation 验证 AI 员工、知识库、待补知识与电脑接口在分发层按契约校验路径参数与请求字段。
func TestAIKnowledgeRequestValidation(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "AI 请求校验", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"})
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	missingID := uuid.NewV7().String()

	_, err = backend.GetKnowledgeBase(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.StopGroupAgentReply(ctx, meta, missingID, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.CreateKnowledgeBase(ctx, meta, appservice.KnowledgeBaseInput{
		Name: " ", Category: "faq", EmbeddingModelID: "bad", EmbeddingDimension: 100, RetrievalCount: 21, RetrievalScoreThreshold: 1.5,
	})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	servertest.RequireFieldError(t, err, "name", i18n.FieldKnowledgeBaseNameRequired)
	servertest.RequireFieldError(t, err, "category", i18n.FieldKnowledgeBaseCategoryInvalid)
	servertest.RequireFieldError(t, err, "embeddingModelId", i18n.FieldKnowledgeBaseEmbeddingModelInvalid)
	servertest.RequireFieldError(t, err, "embeddingDimension", i18n.FieldKnowledgeBaseEmbeddingDimensionInvalid)
	servertest.RequireFieldError(t, err, "retrievalCount", i18n.FieldKnowledgeBaseRetrievalCountInvalid)
	servertest.RequireFieldError(t, err, "retrievalScoreThreshold", i18n.FieldKnowledgeBaseRetrievalScoreThresholdInvalid)
	servertest.RequireFieldError(t, err, "rerankModelId", i18n.FieldKnowledgeBaseRerankModelInvalid)

	_, err = backend.CreateKnowledgeTextDocument(ctx, meta, missingID, appservice.KnowledgeTextDocumentInput{Title: strings.Repeat("文", 121), Content: " "})
	servertest.RequireFieldError(t, err, "title", i18n.FieldKnowledgeDocumentTitleTooLong)
	servertest.RequireFieldError(t, err, "content", i18n.FieldKnowledgeDocumentContentRequired)

	_, err = backend.RetrieveKnowledgeBase(ctx, meta, missingID, appservice.KnowledgeRetrievalInput{Query: strings.Repeat("问", 251)})
	servertest.RequireFieldError(t, err, "query", i18n.FieldKnowledgeRetrievalQueryInvalid)

	err = backend.AcceptKnowledgeGap(ctx, meta, missingID, appservice.KnowledgeGapAcceptInput{KnowledgeBaseID: "bad", EntryID: "bad", Entry: appservice.KnowledgeQAInput{Answer: "可以退款"}})
	servertest.RequireFieldError(t, err, "knowledgeBaseId", i18n.ErrorKnowledgeBaseNotFound)
	servertest.RequireFieldError(t, err, "entryId", i18n.ErrorKnowledgeQANotFound)
	servertest.RequireFieldError(t, err, "entry.question", i18n.FieldKnowledgeQAQuestionRequired)

	_, err = backend.ListKnowledgeGaps(ctx, meta, appservice.KnowledgeGapListInput{ChannelID: "bad", Status: "pending"})
	servertest.RequireFieldError(t, err, "channelId", i18n.ErrorChannelNotFound)

	_, err = backend.UpdateAgent(ctx, meta, missingID, appservice.UpdateAgentInput{
		DisplayName: "小鹿", TeamIDs: []string{missingID, "bad"}, ServiceAudiences: []appservice.ServiceAudience{"partner"}, WorkStatus: "busy",
	})
	servertest.RequireFieldError(t, err, "teamIds[1]", i18n.FieldTeamInvalid)
	servertest.RequireFieldError(t, err, "serviceAudiences[0]", i18n.FieldServiceAudienceInvalid)
	servertest.RequireFieldError(t, err, "workStatus", i18n.FieldWorkStatusInvalid)

	_, err = backend.CreateAgent(ctx, meta, appservice.CreateAgentInput{
		DisplayName: "小鹿", Execution: appservice.AgentExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &appservice.AgentManagedExecutionInput{
			ModelID: "bad", SystemInstruction: strings.Repeat("指", 20001), KnowledgeBaseIDs: []string{"bad"},
		}},
	})
	servertest.RequireFieldError(t, err, "execution.managed.modelId", i18n.FieldChatModelInvalid)
	servertest.RequireFieldError(t, err, "execution.managed.systemInstruction", i18n.FieldAgentSystemInstructionTooLong)
	servertest.RequireFieldError(t, err, "execution.managed.knowledgeBaseIds[0]", i18n.FieldAgentKnowledgeBaseInvalid)

	_, err = backend.UpdateAgentMemory(ctx, meta, missingID, missingID, appservice.AgentMemoryInput{Name: " ", Description: "说明", Body: strings.Repeat("忆", 2001)})
	servertest.RequireFieldError(t, err, "name", i18n.FieldMemoryNameRequired)
	servertest.RequireFieldError(t, err, "body", i18n.FieldMemoryBodyTooLong)

	_, err = backend.GenerateServiceReplySuggestions(ctx, meta, missingID, appservice.ServiceReplySuggestionsInput{AgentIdentityID: "bad", Mode: "draft", Tone: "loud"})
	servertest.RequireFieldError(t, err, "agentIdentityId", i18n.FieldAgentIdentityIDInvalid)
	servertest.RequireFieldError(t, err, "mode", i18n.FieldCustomerReplyModeInvalid)
	servertest.RequireFieldError(t, err, "tone", i18n.FieldCustomerReplyToneInvalid)

	_, err = backend.CreateAgentEvaluationCase(ctx, meta, missingID, appservice.AgentEvaluationCaseInput{Audience: domain.ServiceAudienceCustomer, Question: " ", ExpectedAction: "ignore"})
	servertest.RequireFieldError(t, err, "question", i18n.FieldAgentEvaluationQuestionRequired)
	servertest.RequireFieldError(t, err, "expectedAction", i18n.FieldAgentEvaluationExpectedActionInvalid)

	_, err = backend.RegisterComputer(ctx, meta, appservice.ComputerRegistrationInput{InstallID: " ", Name: strings.Repeat("机", 101)})
	servertest.RequireFieldError(t, err, "installId", i18n.FieldRequired)
	servertest.RequireFieldError(t, err, "name", i18n.FieldComputerNameTooLong)

	_, err = backend.CreateBusinessSystem(ctx, meta, appservice.BusinessSystemInput{Name: " "})
	servertest.RequireFieldError(t, err, "name", i18n.FieldBusinessSystemNameRequired)
}

// TestAccountPlatformRequestValidation 验证账号、工作区与平台领域的契约校验：路径编号不符合规则时按资源不存在返回，请求体与查询字段按标签返回字段文案。
func TestAccountPlatformRequestValidation(t *testing.T) {
	t.Parallel()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ctx := context.Background()
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "平台校验", DisplayName: "负责人", Email: servertest.UniqueEmail("owner"), Password: "password123"})
	_, err = db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_platform_admin = true").Where("id = ?", owner.Identity.Account.ID).Exec(ctx)
	require.NoError(t, err)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: owner.Token, WorkspaceID: owner.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	adminMeta := appservice.RequestMeta{Token: owner.Token, Locale: domain.LocaleChineseSimplified}

	_, err = backend.GetTeam(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.GetRole(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.GetUser(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	err = backend.RevokeInvitation(ctx, meta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.DeactivatePlatformAccount(ctx, adminMeta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.SuspendPlatformWorkspace(ctx, adminMeta, "not-a-uuid")
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)

	_, err = backend.CreateTeam(ctx, meta, appservice.TeamInput{Name: " ", Description: strings.Repeat("团", 501)})
	servertest.RequireFieldError(t, err, "name", i18n.FieldTeamNameRequired)
	servertest.RequireFieldError(t, err, "description", i18n.FieldTeamDescriptionTooLong)
	_, err = backend.CreateTeam(ctx, meta, appservice.TeamInput{Name: strings.Repeat("团", 65)})
	servertest.RequireFieldError(t, err, "name", i18n.FieldTeamNameTooLong)

	_, err = backend.UpdateUserWorkStatus(ctx, meta, appservice.UserWorkStatusInput{WorkStatus: "busy"})
	servertest.RequireFieldError(t, err, "workStatus", i18n.FieldWorkStatusInvalid)
	_, err = backend.UpdateUser(ctx, meta, owner.Identity.User.ID, appservice.UpdateUserInput{DisplayName: "负责人", RoleID: "bad", TeamIDs: []string{uuid.NewV7().String(), "bad"}})
	servertest.RequireFieldError(t, err, "roleId", i18n.FieldMemberRoleInvalid)
	servertest.RequireFieldError(t, err, "teamIds[1]", i18n.FieldTeamInvalid)
	_, err = backend.ListUsers(ctx, meta, appservice.UserListInput{RoleID: "bad", Page: 1, PageSize: 50})
	servertest.RequireFieldError(t, err, "roleId", i18n.FieldIDInvalid)

	_, err = backend.GetTeamPerformanceReport(ctx, meta, appservice.TeamPerformanceReportInput{Days: 30, ChannelID: "bad", TeamID: "bad"})
	servertest.RequireFieldError(t, err, "channelId", i18n.ErrorChannelNotFound)
	servertest.RequireFieldError(t, err, "teamId", i18n.ErrorTeamNotFound)

	_, err = backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{RegistrationPolicy: "closed", WorkspaceCreationPolicy: "nobody"})
	servertest.RequireFieldError(t, err, "registrationPolicy", i18n.FieldRegistrationPolicyInvalid)
	servertest.RequireFieldError(t, err, "workspaceCreationPolicy", i18n.FieldWorkspaceCreationPolicyInvalid)
	_, err = backend.ListPlatformAccounts(ctx, adminMeta, appservice.PlatformAccountListInput{Status: "deleted", Page: 1, PageSize: 50})
	servertest.RequireFieldError(t, err, "status", i18n.FieldUserStatusInvalid)
	_, err = backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Status: "deleted", Sort: "name", Page: 1, PageSize: 50})
	servertest.RequireFieldError(t, err, "status", i18n.FieldPlatformQueryInvalid)
	servertest.RequireFieldError(t, err, "sort", i18n.FieldPlatformQueryInvalid)
	_, err = backend.ListPlatformWorkspaceUsage(ctx, adminMeta, appservice.PlatformWorkspaceUsageListInput{Days: 0, Sort: "members", Page: 1, PageSize: 50})
	servertest.RequireFieldError(t, err, "days", i18n.FieldPlatformQueryInvalid)
	servertest.RequireFieldError(t, err, "sort", i18n.FieldPlatformQueryInvalid)
	_, err = backend.ListPlatformServerLogs(ctx, adminMeta, appservice.PlatformServerLogListInput{MinLevel: "trace"})
	servertest.RequireFieldError(t, err, "minLevel", i18n.FieldPlatformQueryInvalid)
}
