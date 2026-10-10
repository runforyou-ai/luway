//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestCustomerAgentWorkspaceComputer 验证服务客户的 AI 员工使用工作区电脑：客户会话中只挂载不需要发起人确认的电脑工具，
// 需要审批的工具由负责人审批，文件操作限定在会话文件夹内。
func TestCustomerAgentWorkspaceComputer(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "资料客服", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, Execution: execution,
	})
	require.NoError(t, err)
	added, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, identity, "资料服务器")
	require.NoError(t, err)
	_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, agentaction.UpdateInput{
		DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: agent.ServiceAudiences, ResponsibleUserID: identity.User.ID,
		ComputerID: added.Record.ID, ComputerGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL3, ConfirmL2: true}, WorkStatus: domain.WorkStatusWorking,
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "资料咨询", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	received, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{}).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ClientMessageID: uuid.NewV7().String(), Body: "帮我查一下资料",
		ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""),
	})
	require.NoError(t, err)
	inspect := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		require.Contains(t, request.Assignment.Tools, "read_file")
		require.Contains(t, request.Assignment.Tools, "execute")
		require.NotContains(t, request.Assignment.Tools, "write_file", "confirmation tool mounted in customer session")
		require.Equal(t, []domain.ToolIntervention{domain.ToolInterventionApproval}, request.ComputerAccess.Interventions)
		target := request.Computer.Target(domain.ComputerOperation{Kind: domain.ComputerOperationReadFile, Path: "notes.txt"})
		require.Equal(t, added.Record.ID, target.ComputerID)
		require.True(t, target.Operation.Confined, "customer file operation confined")
		require.Equal(t, received.Conversation.ID, target.Operation.Folder)
		return completeTestRun(ctx, feed, "好的")
	}}
	run := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", received.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx))
	require.NoError(t, newTestAgentRun(db, tasks, inspect, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
}
