//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/stream"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testAgents 覆盖 AI 员工的创建、执行配置修订、状态切换、团队与渠道联动及团队删除。
func (s *serverActionsFixture) testAgents(t *testing.T) {
	db, loggedIn, updateChannel := s.db, s.loggedIn, s.updateChannel
	provider := &servermodels.AIProvider{
		WorkspaceID:    &loggedIn.Identity.Workspace.ID,
		Brand:          string(domain.AIProviderBrandOpenAI),
		Name:           "测试模型服务",
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey),
		APIKey:         "test-key",
		APIURL:         "https://example.com/v1",
	}
	_, err := db.NewInsert().Model(provider).
		Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").
		Returning("id").
		Exec(context.Background())
	require.NoError(t, err)
	model := &testAIModel{
		ProviderID: provider.ID, Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	insertAIModels(t, db, model)
	createdAgent, err := agentaction.NewCreateAgentAction(db).Execute(context.Background(), loggedIn.Identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "接待智能体",
		TeamIDs: []string{s.team.ID},
		Execution: agentaction.ExecutionInput{
			Mode: domain.AgentExecutionModeManaged,
			Managed: &agentaction.ManagedExecutionInput{
				ModelID: model.ID, SystemInstruction: "负责接待客户。",
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, createdAgent.Teams, 1, "created agent teams")
	require.Equal(t, s.team.ID, createdAgent.Teams[0].ID, "created agent team")
	require.False(t, createdAgent.CreatedAt.IsZero(), "created agent created at")
	require.NotNil(t, createdAgent.Execution.Managed, "created agent managed execution")
	require.Equal(t, model.ID, createdAgent.Execution.Managed.Model.ID, "created agent model")
	serviceAssignees, err := inboxaction.NewListServiceAssigneesQuery(db).Execute(context.Background(), loggedIn.Identity)
	require.NoError(t, err)
	var agentListedAsCustomerService bool
	for _, assignee := range serviceAssignees {
		if assignee.IdentityID == createdAgent.IdentityID && assignee.Type == domain.WorkspaceIdentityTypeAgent {
			agentListedAsCustomerService = true
			break
		}
	}
	require.True(t, agentListedAsCustomerService, "AI customer service missing from assignees: %#v", serviceAssignees)
	originalRevisionID := createdAgent.Execution.RevisionID
	agentWithUpdatedExecution, err := agentaction.NewUpdateExecutionAction(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateExecutionInput{ExecutionInput: agentaction.ExecutionInput{
		Mode: domain.AgentExecutionModeManaged,
		Managed: &agentaction.ManagedExecutionInput{
			ModelID: model.ID, SystemInstruction: "负责接待并回答客户问题。",
		},
	}})
	require.NoError(t, err)
	require.NotEqual(t, originalRevisionID, agentWithUpdatedExecution.Execution.RevisionID, "updated agent execution revision")
	require.Equal(t, "负责接待并回答客户问题。", agentWithUpdatedExecution.Execution.Managed.SystemInstruction, "updated agent execution instruction")
	revisionCount, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).
		Where("workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("agent_id = ?", createdAgent.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), revisionCount, "agent revision count")
	require.NotEmpty(t, createdAgent.IdentityID, "agent identity id")
	require.NotEqual(t, createdAgent.ID, createdAgent.IdentityID, "agent identity id")
	agents, err := agentaction.NewListAgentsQuery(db).Execute(context.Background(), loggedIn.Identity, agentaction.ListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, agents.Page.Total, "agent directory total")
	require.Len(t, agents.Agents, 1, "agent directory")
	require.Equal(t, createdAgent.ID, agents.Agents[0].ID, "agent directory")
	updatedAgent, err := agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{
		DisplayName:      "售前智能体",
		TeamIDs:          []string{s.team.ID},
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer},
		WorkStatus:       domain.WorkStatusAway,
	})
	require.NoError(t, err)
	require.Equal(t, "售前智能体", updatedAgent.DisplayName, "updated agent")
	require.Equal(t, domain.WorkStatusAway, updatedAgent.WorkStatus, "updated agent")
	agent, err := agentaction.NewGetAgentQuery(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID)
	require.NoError(t, err)
	require.Equal(t, "售前智能体", agent.DisplayName, "agent detail")
	require.Equal(t, domain.WorkStatusAway, agent.WorkStatus, "agent detail")
	// 核验不存在的所属团队触发整次资料提交回滚。
	_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{
		DisplayName: "不应保存的名称", TeamIDs: []string{uuid.NewV7().String()}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking,
	})
	require.Error(t, err, "missing agent team update succeeded")
	var missingTeamError *common.FieldError
	require.ErrorAs(t, err, &missingTeamError, "missing agent team error")
	require.Equal(t, agentaction.ValidationTeamInvalid, missingTeamError.Fields["teamIds"], "missing agent team error")
	agent, err = agentaction.NewGetAgentQuery(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID)
	require.NoError(t, err)
	require.Equal(t, "售前智能体", agent.DisplayName, "agent after rejected update")
	require.Equal(t, domain.WorkStatusAway, agent.WorkStatus, "agent after rejected update")
	teamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.MemberListInput{WorkStatus: domain.WorkStatusAway, Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, teamMembers.Page.Total, "team directory total")
	require.Len(t, teamMembers.Members, 1, "team directory")
	require.Equal(t, createdAgent.IdentityID, teamMembers.Members[0].IdentityID, "team directory")
	require.Equal(t, domain.WorkStatusAway, teamMembers.Members[0].WorkStatus, "team directory")
	s.channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	require.NotNil(t, s.channel.InitialRoutingTargetID, "agent channel routing")
	require.Equal(t, createdAgent.IdentityID, *s.channel.InitialRoutingTargetID, "agent channel routing")
	_, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, s.telegramChannel.ID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err, "Telegram agent route")

	taskRuntime := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(taskRuntime)
	coordinator := newTestAgentRun(db, taskRuntime, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	claimServiceSession := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, testEnqueuer)
	transferServiceSession := servicesessionaction.NewTransferServiceSessionAction(db, coordinator, scheduler, testEnqueuer)
	closeServiceSession := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer)
	_, err = claimServiceSession.Execute(context.Background(), loggedIn.Identity, s.telegramConversationID)
	require.NoError(t, err)
	_, err = transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: s.telegramConversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
	})
	require.NoError(t, err, "Telegram agent transfer")
	s.channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	publicQueueInbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: s.channel.ID, ExternalID: "web-session:fedcba9876543210fedcba9876543210",
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f92", Body: "需要人工接待",
	})
	require.NoError(t, err)
	sendCustomerMessage := servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer)
	_, err = sendCustomerMessage.Execute(context.Background(), loggedIn.Identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: publicQueueInbound.Conversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f93", Body: "我来处理",
	})
	require.NoError(t, err)
	memberHistory, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(context.Background(), customerchataction.MessageHistoryInput{
		ChannelID: s.channel.ID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ConversationID: publicQueueInbound.Conversation.ID,
	})
	// 成员回复前自动领取周期，访客在回复前看到成员加入事件。
	require.NoError(t, err)
	require.Len(t, memberHistory.Messages, 3, "website visitor and human messages")
	require.Nil(t, memberHistory.Messages[0].SenderIdentityType, "website visitor sender identity")
	require.NotNil(t, memberHistory.Messages[1].Event, "member joined event")
	require.Equal(t, customerchataction.VisitorEventMemberJoined, memberHistory.Messages[1].Event.Type, "member joined event")
	require.Equal(t, domain.MessageAuthorAgent, memberHistory.Messages[2].Author, "human reply author")
	require.NotNil(t, memberHistory.Messages[2].SenderIdentityType, "human sender identity")
	require.Equal(t, domain.WorkspaceIdentityTypeUser, *memberHistory.Messages[2].SenderIdentityType, "human sender identity")
	memberSummariesDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(context.Background(), s.channel.ID, "web-session:fedcba9876543210fedcba9876543210")
	memberSummaries := memberSummariesDirectory.Conversations
	require.NoError(t, err)
	require.Len(t, memberSummaries, 1, "website human preview")
	require.NotNil(t, memberSummaries[0].PreviewSenderIdentityType, "website human preview identity")
	require.Equal(t, domain.WorkspaceIdentityTypeUser, *memberSummaries[0].PreviewSenderIdentityType, "website human preview identity")
	publicQueueSession := &servermodels.ServiceSession{}
	require.NoError(t, db.NewSelect().Model(publicQueueSession).
		Where("ss.id = ?", publicQueueInbound.Conversation.ServiceSessionID).
		Scan(context.Background()))
	require.NotNil(t, publicQueueSession.AssigneeIdentityID, "public queue reply session")
	require.Equal(t, loggedIn.Identity.WorkspaceIdentity.ID, *publicQueueSession.AssigneeIdentityID, "public queue reply session")
	closedPublicQueue, err := closeServiceSession.Execute(context.Background(), loggedIn.Identity, publicQueueInbound.Conversation.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ServiceSessionStatusClosed, closedPublicQueue.Status, "closed public queue session")
	s.channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)

	websiteMessageInput := customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: s.channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f80", Body: "需要 AI 接待",
	}
	websiteInbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), websiteMessageInput)
	require.NoError(t, err)
	websiteRetried, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), websiteMessageInput)
	require.NoError(t, err)
	require.Equal(t, websiteInbound.Message.ID, websiteRetried.Message.ID, "idempotent website message")
	websiteSession := &servermodels.ServiceSession{}
	require.NoError(t, db.NewSelect().Model(websiteSession).
		Where("ss.id = ?", websiteInbound.Conversation.ServiceSessionID).
		Scan(context.Background()))
	require.NotNil(t, websiteSession.AssigneeIdentityID, "website agent route session")
	require.Equal(t, createdAgent.IdentityID, *websiteSession.AssigneeIdentityID, "website agent route session")
	inboxQuery := inboxaction.NewLoadInboxQuery(db)
	allBeforeWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
	allBeforeWebsiteClaim := allBeforeWebsiteClaimPage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, allBeforeWebsiteClaim, websiteInbound.Conversation.ID, false)
	coworkerInboxBeforeWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{
		Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: createdAgent.IdentityID,
	})
	coworkerInboxBeforeWebsiteClaim := coworkerInboxBeforeWebsiteClaimPage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, coworkerInboxBeforeWebsiteClaim, websiteInbound.Conversation.ID, true)
	websiteTriggerCount, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		Join("JOIN agent_lanes al ON al.id = ai.lane_id").
		Where("al.conversation_id = ?", websiteInbound.Conversation.ID).
		Count(context.Background())
	require.NoError(t, err)
	websiteRunCount, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), websiteTriggerCount, "website agent route triggers")
	require.Equal(t, int64(1), websiteRunCount, "website agent route runs")
	initialWebsiteRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(initialWebsiteRun).
		Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	require.Equal(t, string(domain.AgentExecutionScopeServiceSession), initialWebsiteRun.ScopeKind, "initial website agent run")
	require.Equal(t, websiteSession.ID, initialWebsiteRun.ScopeID, "initial website agent run")
	require.Equal(t, int64(1), initialWebsiteRun.InputStartSeq, "initial website agent run")
	require.Nil(t, initialWebsiteRun.InputEndSeq, "initial website agent run")
	claimedWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	require.NotNil(t, claimedWebsite.Assignee, "claim agent session without state")
	require.Equal(t, loggedIn.Identity.WorkspaceIdentity.ID, claimedWebsite.Assignee.IdentityID, "claim agent session without state")
	allAfterWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
	allAfterWebsiteClaim := allAfterWebsiteClaimPage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, allAfterWebsiteClaim, websiteInbound.Conversation.ID, true)
	transferredWithoutReply, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
	})
	require.NoError(t, err)
	require.NotNil(t, transferredWithoutReply.Assignee, "transfer unparticipated website session")
	require.Equal(t, createdAgent.IdentityID, transferredWithoutReply.Assignee.IdentityID, "transfer unparticipated website session")
	allAfterTransferWithoutReplyPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
	allAfterTransferWithoutReply := allAfterTransferWithoutReplyPage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, allAfterTransferWithoutReply, websiteInbound.Conversation.ID, false)
	reclaimedWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	require.NotNil(t, reclaimedWebsite.Assignee, "reclaim website session before reply")
	require.Equal(t, loggedIn.Identity.WorkspaceIdentity.ID, reclaimedWebsite.Assignee.IdentityID, "reclaim website session before reply")
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: websiteInbound.Conversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f91", Body: "我已参与处理",
	})
	require.NoError(t, err)
	transferredWebsite, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
	})
	require.NoError(t, err)
	require.NotNil(t, transferredWebsite.Assignee, "transfer website session to agent")
	require.Equal(t, createdAgent.IdentityID, transferredWebsite.Assignee.IdentityID, "transfer website session to agent")
	allAfterWebsiteTransferPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
	allAfterWebsiteTransfer := allAfterWebsiteTransferPage.Conversations
	require.NoError(t, err)
	// 本人回复后转给 AI 员工，会话由 AI 员工负责，不再需要本人处理。
	assertInboxConversationPresence(t, allAfterWebsiteTransfer, websiteInbound.Conversation.ID, false)
	updatedAgent, err = agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	require.Equal(t, domain.IdentityStatusInactive, updatedAgent.Status, "inactive agent")
	require.Equal(t, domain.WorkStatusOffDuty, updatedAgent.WorkStatus, "inactive agent")
	teamMembersAfterAgentDeactivation, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, teamMembersAfterAgentDeactivation.Page.Total, "team members after agent deactivation")
	require.Len(t, teamMembersAfterAgentDeactivation.Members, 1, "team members after agent deactivation")
	require.Equal(t, s.createdMember.IdentityID, teamMembersAfterAgentDeactivation.Members[0].IdentityID, "team members after agent deactivation")
	teamAfterAgentDeactivation, err := teamaction.NewUpdateTeamAction(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.Input{Name: s.team.Name, Description: s.team.Description})
	require.NoError(t, err)
	require.Equal(t, 1, teamAfterAgentDeactivation.MemberCount, "team after agent deactivation")
	_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{DisplayName: updatedAgent.DisplayName, TeamIDs: []string{s.team.ID}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking})
	require.Error(t, err, "inactive agent work status update succeeded")
	var unavailableWorkStatusError *common.FieldError
	require.ErrorAs(t, err, &unavailableWorkStatusError, "inactive agent work status error")
	require.Equal(t, agentaction.ValidationWorkStatusUnavailable, unavailableWorkStatusError.Fields["workStatus"], "inactive agent work status error")
	updatedAgent, err = agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, domain.IdentityStatusActive)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStatusOffDuty, updatedAgent.WorkStatus, "reactivated agent work status")
	teamAfterAgentReactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, teamAfterAgentReactivation.Teams, 1, "team after agent reactivation")
	require.Equal(t, 2, teamAfterAgentReactivation.Teams[0].MemberCount, "team after agent reactivation")
	updatedAgent, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{DisplayName: updatedAgent.DisplayName, TeamIDs: []string{s.team.ID}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStatusWorking, updatedAgent.WorkStatus, "working agent")
	_, err = claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	_, err = transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
	})
	require.NoError(t, err)

	agentStart, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(context.Background(), loggedIn.Identity, directchataction.FirstAgentTextMessageInput{ConversationID: "0198ddf0-a234-7f01-8d99-e3e0af0f5fff",
		AgentIdentityID: createdAgent.IdentityID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f70", Body: "计算 6 乘以 7",
	})
	require.NoError(t, err)
	require.Equal(t, createdAgent.IdentityID, agentStart.Conversation.Agent.AgentIdentityID, "agent direct conversation")
	agentConversation := agentStart.Conversation
	sendAgentMessage := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, scheduler)
	_, err = sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
		ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f71", Body: "只给我最终结果",
	})
	require.NoError(t, err)

	state := &servermodels.AgentLane{}
	require.NoError(t, db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	triggerCount, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).Join("JOIN agent_lanes al ON al.id = ai.lane_id").Where("al.conversation_id = ?", agentConversation.ID).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), state.DesiredSeq, "scheduled agent input desired seq")
	require.Equal(t, int64(0), state.ProcessedSeq, "scheduled agent input processed seq")
	require.Equal(t, int64(2), triggerCount, "scheduled agent input triggers")
	run := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	require.Equal(t, string(domain.AgentExecutionScopeConversation), run.ScopeKind, "direct run scope kind")
	require.Equal(t, agentConversation.ID, run.ScopeID, "direct run scope id")
	websiteConversationID := websiteInbound.Conversation.ID
	_, err = customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: s.channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
		ConversationID: &websiteConversationID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f81", Body: "接管前的新问题",
	})
	require.NoError(t, err)
	customerState := &servermodels.AgentLane{}
	require.NoError(t, db.NewSelect().Model(customerState).
		Where("al.conversation_id = ?", websiteInbound.Conversation.ID).
		Where("al.agent_identity_id = ?", createdAgent.IdentityID).
		Scan(context.Background()))
	customerRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(customerRun).
		Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
		Where("agr.agent_identity_id = ?", createdAgent.IdentityID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	require.Equal(t, int64(3), customerState.DesiredSeq, "scheduled customer follow-up state")
	require.Equal(t, int64(2), customerState.ProcessedSeq, "scheduled customer follow-up state")
	require.Equal(t, int64(3), customerRun.InputStartSeq, "scheduled customer follow-up run")
	require.Nil(t, customerRun.InputEndSeq, "scheduled customer follow-up run")
	_, err = closeServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	var closeOwnedConflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &closeOwnedConflict, "close agent-owned session error")
	require.Equal(t, conversationaction.ConflictReasonServiceSessionOwned, closeOwnedConflict.Reason, "close agent-owned session error")
	require.NoError(t, db.NewSelect().Model(customerRun).Where("agr.id = ?", customerRun.ID).Scan(context.Background()))
	require.Equal(t, string(domain.AgentRunStatusQueued), customerRun.Status, "run changed by unauthorized close")
	require.Nil(t, customerRun.ErrorCode, "run changed by unauthorized close")
	claimedRunningWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	require.NotNil(t, claimedRunningWebsite.Assignee, "claim running agent session")
	require.Equal(t, loggedIn.Identity.WorkspaceIdentity.ID, claimedRunningWebsite.Assignee.IdentityID, "claim running agent session")
	require.NoError(t, db.NewSelect().Model(customerRun).Where("agr.id = ?", customerRun.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(customerState).
		Where("al.conversation_id = ?", customerState.ConversationID).
		Where("al.agent_identity_id = ?", customerState.AgentIdentityID).
		Scan(context.Background()))
	require.Equal(t, string(domain.AgentRunStatusCancelled), customerRun.Status, "cancelled customer run")
	require.NotNil(t, customerRun.ErrorCode, "cancelled customer run")
	require.Equal(t, string(domain.AgentRunErrorCodeAssigneeChanged), *customerRun.ErrorCode, "cancelled customer run")
	require.Equal(t, int64(3), customerState.ProcessedSeq, "cancelled customer state")
	require.Equal(t, customerState.DesiredSeq, customerState.ProcessedSeq, "cancelled customer state")
	require.NoError(t, coordinator.Execute(context.Background(), agentrunaction.RunInput{RunID: customerRun.ID}), "cancelled run retry")
	require.NoError(t, coordinator.FinalizeFailure(context.Background(), agentrunaction.RunInput{RunID: customerRun.ID}, errors.New("task retry exhausted")), "cancelled run finalizer")
	transferredForCustomerRun, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
	})
	require.NoError(t, err)
	require.NotNil(t, transferredForCustomerRun.Assignee, "transfer website session for customer run")
	require.Equal(t, createdAgent.IdentityID, transferredForCustomerRun.Assignee.IdentityID, "transfer website session for customer run")
	absorbingCustomerRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(absorbingCustomerRun).
		Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
		Where("agr.agent_identity_id = ?", createdAgent.IdentityID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	customerRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		pending, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if len(pending) != 1 || pending[0].Seq != 4 {
			return agentruntime.RunResult{}, fmt.Errorf("unexpected initial customer trigger: %#v", pending)
		}
		claimed, err := feed.Claim(ctx, pending[0].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if claimed.EndSeq != 4 || len(claimed.Messages) == 0 || claimed.Messages[len(claimed.Messages)-1].Content != "接管前的新问题" {
			return agentruntime.RunResult{}, fmt.Errorf("unexpected customer claim: %#v", claimed)
		}
		return agentruntime.RunResult{Content: "已回复新问题", EndSeq: claimed.EndSeq, Usage: agentcontract.Usage{TotalTokens: 18}}, nil
	}}
	require.NoError(t, newTestAgentRun(db, taskRuntime, customerRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: absorbingCustomerRun.ID}))
	require.NoError(t, db.NewSelect().Model(absorbingCustomerRun).Where("agr.id = ?", absorbingCustomerRun.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(customerState).
		Where("al.conversation_id = ?", customerState.ConversationID).
		Where("al.agent_identity_id = ?", customerState.AgentIdentityID).
		Scan(context.Background()))
	queuedCustomerRuns, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, string(domain.AgentRunStatusSucceeded), absorbingCustomerRun.Status, "absorbed customer run")
	require.Equal(t, int64(4), absorbingCustomerRun.InputStartSeq, "absorbed customer run")
	require.NotNil(t, absorbingCustomerRun.InputEndSeq, "absorbed customer run")
	require.Equal(t, int64(4), *absorbingCustomerRun.InputEndSeq, "absorbed customer run")
	require.Equal(t, int64(4), customerState.DesiredSeq, "absorbed customer state")
	require.Equal(t, int64(4), customerState.ProcessedSeq, "absorbed customer state")
	require.Equal(t, int64(0), queuedCustomerRuns, "absorbed customer active runs")
	websiteMessages, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(context.Background(), customerchataction.MessageHistoryInput{
		ChannelID: s.channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: websiteInbound.Conversation.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, websiteMessages.Messages, "website messages after customer run")
	lastWebsiteMessage := websiteMessages.Messages[len(websiteMessages.Messages)-1]
	require.Equal(t, domain.MessageAuthorAgent, lastWebsiteMessage.Author, "website messages after customer run")
	require.Equal(t, "已回复新问题", lastWebsiteMessage.Body, "website messages after customer run")
	require.NotNil(t, lastWebsiteMessage.SenderIdentityType, "website messages after customer run")
	require.Equal(t, domain.WorkspaceIdentityTypeAgent, *lastWebsiteMessage.SenderIdentityType, "website messages after customer run")
	websiteSummariesDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(context.Background(), s.channel.ID, "web-session:0123456789abcdef0123456789abcdef")
	websiteSummaries := websiteSummariesDirectory.Conversations
	require.NoError(t, err)
	require.NotEmpty(t, websiteSummaries, "website AI preview")
	require.Equal(t, websiteInbound.Conversation.ID, websiteSummaries[0].ID, "website AI preview")
	require.NotNil(t, websiteSummaries[0].PreviewSenderIdentityType, "website AI preview identity")
	require.Equal(t, domain.WorkspaceIdentityTypeAgent, *websiteSummaries[0].PreviewSenderIdentityType, "website AI preview identity")
	_, err = claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	require.NoError(t, db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()))
	require.Equal(t, string(domain.AgentRunStatusQueued), run.Status, "direct run after customer takeover")

	runTasks := make([]servertest.Task, 0)
	for _, task := range taskRuntime.Queued(agentrunaction.RunActionName, loggedIn.Identity.Workspace.ID) {
		if task.Options.IdempotencyKey == "agent:"+run.ID {
			runTasks = append(runTasks, task)
		}
	}
	require.Len(t, runTasks, 1, "agent task run")
	require.Equal(t, 3, runTasks[0].Options.MaxAttempts, "agent task run")
	inboxBeforeRunPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	inboxBeforeRun := inboxBeforeRunPage.Conversations
	require.NoError(t, err)
	assertDirectAgentRunStatus(t, inboxBeforeRun, agentConversation.ID, domain.AgentRunStatusQueued, "只给我最终结果")

	var executionStreams []string
	var successfulBlocks []agentcontract.Block
	executedRuntime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		executionStreams = append(executionStreams, request.StreamID)
		if request.Assignment.AgentName != "售前智能体" || request.Assignment.Model.ModelID != model.ID || request.Models == nil {
			return agentruntime.RunResult{}, errors.New("unexpected agent runtime request")
		}
		pending, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if len(pending) != 1 || pending[0].Seq != 2 {
			return agentruntime.RunResult{}, errors.New("unexpected pending agent triggers")
		}
		firstClaim, err := feed.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if firstClaim.EndSeq != 1 || len(firstClaim.Messages) != 1 {
			return agentruntime.RunResult{}, errors.New("agent claim exceeded requested boundary")
		}
		claimed, err := feed.Claim(ctx, pending[0].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if claimed.EndSeq != 2 || len(claimed.Messages) != 2 {
			return agentruntime.RunResult{}, errors.New("unexpected claimed agent input")
		}
		successfulBlocks = []agentcontract.Block{{
			ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
			Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "计算过程"},
		}}
		request.OnStream(stream.Delta{Stream: request.StreamID, Sequence: 1, Operations: []stream.Operation{{
			Kind:  stream.OpUpsertBlock,
			Block: &stream.Block{ID: successfulBlocks[0].ID, Position: 1, ModelCallID: successfulBlocks[0].ModelCallID, Kind: stream.KindThinking, Text: "计算过程"},
		}}})
		return agentruntime.RunResult{Content: "结果是 42", EndSeq: claimed.EndSeq, Usage: agentcontract.Usage{TotalTokens: 12}, Blocks: runBlocks(successfulBlocks)}, nil
	}}
	executeAgentRun := newTestAgentRun(db, taskRuntime, executedRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	// 用查询钩子让该会话的 AI 回复写入失败，共享测试库上的消息表不加结构锁。
	responseFailure := &agentResponseFailureHook{conversationID: agentConversation.ID}
	responseFailure.armed.Store(true)
	db.AddQueryHook(responseFailure)
	persistenceErr := executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID})
	require.Error(t, persistenceErr, "agent completion persistence error")
	require.False(t, servertask.IsPermanent(persistenceErr), "agent completion persistence error = %#v", persistenceErr)
	require.NoError(t, db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()))
	messageCountAfterPersistenceError, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", agentConversation.ID).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), state.ProcessedSeq, "agent state after completion persistence error")
	require.Equal(t, string(domain.AgentRunStatusRunning), run.Status, "agent run after completion persistence error")
	require.Equal(t, int64(2), messageCountAfterPersistenceError, "messages after completion persistence error")
	blockCount, err := db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", run.ID).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), blockCount, "blocks after failed transaction")
	responseFailure.armed.Store(false)
	require.NoError(t, executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()))
	messageCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", agentConversation.ID).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), state.ProcessedSeq, "completed agent state")
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status, "completed agent run")
	require.NotNil(t, run.ResponseMessageID, "completed agent run")
	require.Equal(t, int64(3), messageCount, "completed agent run messages")
	var savedBlocks []servermodels.AgentRunBlock
	require.NoError(t, db.NewSelect().Model(&savedBlocks).Where("arb.agent_run_id = ?", run.ID).Order("position ASC").Scan(context.Background()))
	require.Len(t, savedBlocks, 1, "saved blocks")
	require.Equal(t, successfulBlocks[0].ID, savedBlocks[0].ID, "saved blocks")
	require.Equal(t, run.WorkspaceID, savedBlocks[0].WorkspaceID, "saved blocks")
	require.NotNil(t, savedBlocks[0].Content, "saved blocks")
	require.Equal(t, "计算过程", *savedBlocks[0].Content, "saved blocks")
	require.Len(t, executionStreams, 2, "retry streams")
	require.NotEmpty(t, executionStreams[0], "retry streams")
	require.NotEqual(t, executionStreams[0], executionStreams[1], "retry streams")
	require.NoError(t, executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID}))
	require.Len(t, executionStreams, 2, "completed run was recomputed")
	inboxAfterRunPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	inboxAfterRun := inboxAfterRunPage.Conversations
	require.NoError(t, err)
	assertDirectAgentRunStatus(t, inboxAfterRun, agentConversation.ID, domain.AgentRunStatusSucceeded, "结果是 42")
	// 最新窗口和锚点窗口均返回消息所属的运行引用，不将过程附到用户消息。
	for _, anchor := range []string{"", *run.ResponseMessageID} {
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: agentConversation.ID, AroundMessageID: anchor})
		require.NoError(t, err)
		require.Empty(t, history.AgentRuns, "agent message history")
		foundProcess := false
		for _, message := range history.Messages {
			if message.ID != *run.ResponseMessageID {
				require.Nil(t, message.AgentProcess, "user message contains agent process")
				continue
			}
			process := message.AgentProcess
			require.NotNil(t, process, "message agent process")
			require.Equal(t, run.ID, process.ID, "message agent process")
			require.Equal(t, 12, process.Usage.TotalTokens, "message agent process")
			require.GreaterOrEqual(t, process.DurationMilliseconds, int64(0), "message agent process")
			foundProcess = true
		}
		require.True(t, foundProcess, "reply process missing from message window")
	}
	// 过程内容按运行编号单独读取，未知运行不返回详情。
	runProcess, err := processquery.NewGetRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, run.ID)
	require.NoError(t, err)
	require.Equal(t, run.ID, runProcess.ID, "agent run process")
	require.Len(t, runProcess.Blocks, 1, "agent run process")
	require.Equal(t, "计算过程", runProcess.Blocks[0].Payload.Text, "agent run process")
	require.Equal(t, 12, runProcess.Usage.TotalTokens, "agent run process")
	require.GreaterOrEqual(t, runProcess.DurationMilliseconds, int64(0), "agent run process")
	_, err = processquery.NewGetRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, uuid.NewV7().String())
	require.ErrorIs(t, err, processquery.ErrRunProcessUnavailable, "unknown agent run process error")
	// 同企业其他成员没有这条 AI 会话的阅读资格，读不到运行过程。
	outsiderLogin := servertest.LoginMember(t, db, loggedIn.Identity.Workspace.ID, s.createdMember.Email, "password123")
	_, err = processquery.NewGetRunProcessQuery(db).Execute(context.Background(), outsiderLogin.Identity, run.ID)
	require.ErrorIs(t, err, processquery.ErrRunProcessUnavailable, "outsider agent run process error")
	// 运行过程流按同一阅读资格授权，本人取得运行所属会话，未知运行与无资格成员均不返回。
	authorizeRunStream := processquery.NewAuthorizeRunStreamQuery(db)
	streamConversationID, err := authorizeRunStream.Execute(context.Background(), loggedIn.Identity, run.ID)
	require.NoError(t, err)
	require.Equal(t, agentConversation.ID, streamConversationID, "agent run stream conversation")
	_, err = authorizeRunStream.Execute(context.Background(), loggedIn.Identity, uuid.NewV7().String())
	require.ErrorIs(t, err, processquery.ErrRunProcessUnavailable, "unknown agent run stream error")
	_, err = authorizeRunStream.Execute(context.Background(), outsiderLogin.Identity, run.ID)
	require.ErrorIs(t, err, processquery.ErrRunProcessUnavailable, "outsider agent run stream error")

	_, err = sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
		ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f72", Body: "这次模拟模型失败",
	})
	require.NoError(t, err)
	failedRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(failedRun).
		Where("agr.conversation_id = ?", agentConversation.ID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	failingRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		pending, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if len(pending) != 1 || pending[0].Seq != 3 {
			return agentruntime.RunResult{}, errors.New("unexpected failing agent trigger")
		}
		if _, err := feed.Claim(ctx, pending[0].Seq); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{
			Usage: agentcontract.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
			Blocks: runBlocks([]agentcontract.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "中断前的思考"},
			}}),
		}, errors.New("model rejected input")
	}}
	require.Error(t, newTestAgentRun(db, taskRuntime, failingRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: failedRun.ID}), "failing agent run succeeded")
	require.NoError(t, db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(failedRun).Where("agr.id = ?", failedRun.ID).Scan(context.Background()))
	require.Equal(t, int64(3), state.ProcessedSeq, "failed claimed agent state")
	require.Equal(t, string(domain.AgentRunStatusFailed), failedRun.Status, "failed claimed agent run")
	require.NotNil(t, failedRun.InputEndSeq, "failed claimed agent run")
	require.Equal(t, int64(3), *failedRun.InputEndSeq, "failed claimed agent run")
	failedHistory, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: agentConversation.ID})
	require.NoError(t, err)
	require.NotNil(t, failedRun.LastError, "failed run last error")
	require.Empty(t, failedHistory.AgentRuns, "failed run message state")
	// 失败运行的过程内容同样按运行编号读取。
	failedRunProcess, err := processquery.NewGetRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, failedRun.ID)
	require.NoError(t, err)
	require.Len(t, failedRunProcess.Blocks, 1, "failed agent run process")
	require.Equal(t, "中断前的思考", failedRunProcess.Blocks[0].Payload.Text, "failed agent run process")
	require.Equal(t, 7, failedRunProcess.Usage.TotalTokens, "failed agent run process")
	// 运行过程流不限运行状态，失败运行同样按会话阅读资格授权。
	streamConversationID, err = authorizeRunStream.Execute(context.Background(), loggedIn.Identity, failedRun.ID)
	require.NoError(t, err)
	require.Equal(t, agentConversation.ID, streamConversationID, "failed agent run stream conversation")

	_, err = sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
		ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f73", Body: "失败后继续",
	})
	require.NoError(t, err)
	exhaustedRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(exhaustedRun).
		Where("agr.conversation_id = ?", agentConversation.ID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	require.Equal(t, int64(4), exhaustedRun.InputStartSeq, "agent run after failure start seq")
	finalizer := newTestAgentRun(db, taskRuntime, failingRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	require.NoError(t, finalizer.FinalizeFailure(context.Background(), agentrunaction.RunInput{RunID: exhaustedRun.ID}, errors.New("task attempts exhausted")))
	require.NoError(t, db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()))
	require.NoError(t, db.NewSelect().Model(exhaustedRun).Where("agr.id = ?", exhaustedRun.ID).Scan(context.Background()))
	require.Equal(t, int64(4), state.ProcessedSeq, "exhausted agent state")
	require.Equal(t, string(domain.AgentRunStatusFailed), exhaustedRun.Status, "exhausted agent run")
	require.NotNil(t, exhaustedRun.InputEndSeq, "exhausted agent run")
	require.Equal(t, int64(4), *exhaustedRun.InputEndSeq, "exhausted agent run")
	_, err = sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
		ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f74", Body: "耗尽后继续",
	})
	require.NoError(t, err)
	nextRun := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(nextRun).
		Where("agr.conversation_id = ?", agentConversation.ID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(context.Background()))
	require.Equal(t, int64(5), nextRun.InputStartSeq, "agent run after exhausted task")

	testAgentFailureMessages(t, db, loggedIn.Identity, taskRuntime, agentConversation.ID, failedRun.ID, exhaustedRun.ID, nextRun.ID)

	t.Run("Agent 运行期业务系统", func(t *testing.T) {
		testAgentRunBusinessSystems(t, db, loggedIn.Identity, model.ID, taskRuntime)
	})

	closedWebsite, err := closeServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ServiceSessionStatusClosed, closedWebsite.Status, "closed participated website session")
	allAfterWebsiteClosePage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
	allAfterWebsiteClose := allAfterWebsiteClosePage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, allAfterWebsiteClose, websiteInbound.Conversation.ID, false)
	closedInboxPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{
		Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: loggedIn.Identity.WorkspaceIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed,
	})
	closedInbox := closedInboxPage.Conversations
	require.NoError(t, err)
	assertInboxConversationPresence(t, closedInbox, websiteInbound.Conversation.ID, true)

	_, err = useraction.NewUpdateUserAction(db, testServiceSessionReturner(db), testEnqueuer).Execute(context.Background(), loggedIn.Identity, s.createdMember.ID, useraction.UpdateInput{
		DisplayName: s.createdMember.DisplayName, RoleID: s.createdMember.RoleID, TeamIDs: []string{s.team.ID},
	})
	require.NoError(t, err)
	require.NoError(t, teamaction.NewDeleteTeamAction(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, s.team.ID))
	memberAfterTeamDelete, err := useraction.NewGetUserQuery(db).Execute(context.Background(), loggedIn.Identity, s.createdMember.ID)
	require.NoError(t, err)
	require.Empty(t, memberAfterTeamDelete.Teams, "member after team delete")
}

// agentResponseFailureHook 在启用期间以已取消的上下文执行指定会话的 AI 回复写入，使该次写入失败。
type agentResponseFailureHook struct {
	conversationID string
	armed          atomic.Bool
}

// BeforeQuery 对启用期间指定会话带 agent 幂等键的消息写入返回已取消的上下文。
func (h *agentResponseFailureHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if !h.armed.Load() || !strings.HasPrefix(event.Query, `INSERT INTO "messages"`) ||
		!strings.Contains(event.Query, h.conversationID) || !strings.Contains(event.Query, "'agent:") {
		return ctx
	}
	failed, cancel := context.WithCancel(ctx)
	cancel()
	return failed
}

// AfterQuery 不处理查询结果。
func (h *agentResponseFailureHook) AfterQuery(context.Context, *bun.QueryEvent) {}
