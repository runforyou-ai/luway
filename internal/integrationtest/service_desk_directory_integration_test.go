//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestServiceDeskDirectory 验证 AI 员工负责人的保存与校验，以及同事目录中服务台的范围、排序、检索和负责人展示。
func TestServiceDeskDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newMemberFixture(t)
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "chat-a", Name: "对话 A", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 4096,
		}},
	})
	require.NoError(t, err, "创建模型服务失败")
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: aiModelID(t, f.db, provider.ID, "chat-a"), SystemInstruction: "回答同事的问题",
	}}
	createAgent := func(name string, audiences ...domain.ServiceAudience) *agentaction.Agent {
		agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{DisplayName: name, ServiceAudiences: audiences, Execution: execution})
		require.NoError(t, err, "创建 AI 员工失败")
		return agent
	}
	desk := createAgent("IT 服务台", domain.ServiceAudienceEmployee)
	createAgent("官网客服", domain.ServiceAudienceCustomer)
	listColleagues := func(query string) memberaction.ListColleaguesOutput {
		output, err := memberaction.NewListColleaguesQuery(f.db).Execute(ctx, f.member, memberaction.ListColleaguesInput{Query: query, Page: 1, PageSize: 50})
		require.NoError(t, err, "读取同事目录失败")
		return output
	}

	update := agentaction.NewUpdateAgentAction(f.db, testEnqueuer, testServiceSessionReturner(f.db))
	input := agentaction.UpdateInput{
		DisplayName: desk.DisplayName, TeamIDs: []string{}, WorkStatus: domain.WorkStatusWorking,
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, ResponsibleUserID: uuid.NewV7().String(),
	}
	var fieldError *common.FieldError
	_, err = update.Execute(ctx, f.owner, desk.ID, input)
	require.ErrorAs(t, err, &fieldError, "不存在的负责人应被拒绝")
	require.Equal(t, agentaction.ValidationResponsibleInvalid, fieldError.Fields["responsibleUserId"], "不存在的负责人应被拒绝")
	input.ResponsibleUserID = f.member.User.ID
	updated, err := update.Execute(ctx, f.owner, desk.ID, input)
	require.NoError(t, err, "保存负责人失败")
	require.NotNil(t, updated.Responsible)
	require.Equal(t, f.member.User.ID, updated.Responsible.UserID)
	require.Equal(t, "成员", updated.Responsible.DisplayName)
	require.Equal(t, domain.IdentityStatusActive, updated.Responsible.Status)

	// 目录只含在职成员与服务员工的 AI 员工，服务台排在最前并带负责人姓名。
	listed := listColleagues("")
	require.Equal(t, 3, listed.Page.Total)
	require.Len(t, listed.Colleagues, 3)
	first := listed.Colleagues[0]
	require.Equal(t, domain.WorkspaceIdentityTypeAgent, first.IdentityType)
	require.Equal(t, desk.ID, first.AgentID)
	require.Equal(t, "成员", first.ResponsibleName)
	require.Empty(t, first.UserID)
	for _, colleague := range listed.Colleagues[1:] {
		require.Equal(t, domain.WorkspaceIdentityTypeUser, colleague.IdentityType)
		require.NotEmpty(t, colleague.UserID)
		require.NotEmpty(t, colleague.Email)
	}
	searched := listColleagues("服务台")
	require.Len(t, searched.Colleagues, 1)
	require.Equal(t, desk.ID, searched.Colleagues[0].AgentID)

	// 负责人停用后目录不再展示其姓名，保留原负责人时仍可保存其他资料。
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err, "停用负责人失败")
	listed, err = memberaction.NewListColleaguesQuery(f.db).Execute(ctx, f.owner, memberaction.ListColleaguesInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 2, listed.Page.Total)
	require.Equal(t, desk.ID, listed.Colleagues[0].AgentID)
	require.Empty(t, listed.Colleagues[0].ResponsibleName)
	input.DisplayName = "IT 服务台 2"
	updated, err = update.Execute(ctx, f.owner, desk.ID, input)
	require.NoError(t, err, "保留已停用负责人时应可保存")
	require.NotNil(t, updated.Responsible)
	require.Equal(t, f.member.User.ID, updated.Responsible.UserID)
	require.Equal(t, domain.IdentityStatusInactive, updated.Responsible.Status)
	input.ResponsibleUserID = ""
	updated, err = update.Execute(ctx, f.owner, desk.ID, input)
	require.NoError(t, err, "清空负责人失败")
	require.Nil(t, updated.Responsible)
}
