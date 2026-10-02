//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestAgentServiceScope 验证 AI 员工服务对象与转人工团队的保存、校验、接待资格和团队删除后的回退。
func TestAgentServiceScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newNavigationFixture(t)
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "chat-a", Name: "对话 A", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 4096,
		}},
	})
	if err != nil {
		t.Fatalf("创建模型服务失败：%v", err)
	}
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: aiModelID(t, f.db, provider.ID, "chat-a"), SystemInstruction: "回答同事和客户的问题",
	}}
	fieldCode := func(err error, field string) common.FieldCode {
		var fieldError *common.FieldError
		if !errors.As(err, &fieldError) {
			return ""
		}
		return fieldError.Fields[field]
	}
	listedAsAssignee := func(identityID string) bool {
		assignees, err := inboxaction.NewListServiceAssigneesQuery(f.db).Execute(ctx, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(assignees, func(assignee inboxaction.ServiceAssignee) bool { return assignee.IdentityID == identityID })
	}

	if _, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		DisplayName: "伙伴助手", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudiencePartner}, Execution: execution,
	}); fieldCode(err, "serviceAudiences") != agentaction.ValidationServiceAudienceInvalid {
		t.Fatalf("伙伴服务对象应被拒绝：%v", err)
	}
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		DisplayName: "服务台助手", Execution: execution,
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee, domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee},
	})
	if err != nil {
		t.Fatalf("创建 AI 员工失败：%v", err)
	}
	if !slices.Equal(created.ServiceAudiences, []domain.ServiceAudience{domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee}) || created.HandoffTeamID != nil {
		t.Fatalf("created agent = %+v", created)
	}
	if !listedAsAssignee(created.IdentityID) {
		t.Fatal("服务客户的 AI 员工应可接待客户会话")
	}

	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "IT 支持 " + uuid.NewV7().String()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	update := agentaction.NewUpdateAgentAction(f.db, testServiceSessionReturner(f.db))
	input := agentaction.UpdateInput{
		DisplayName: created.DisplayName, TeamIDs: []string{}, WorkStatus: domain.WorkStatusWorking,
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, HandoffTeamID: uuid.NewV7().String(),
	}
	if _, err := update.Execute(ctx, f.owner, created.ID, input); fieldCode(err, "handoffTeamId") != agentaction.ValidationHandoffTeamInvalid {
		t.Fatalf("不存在的转人工团队应被拒绝：%v", err)
	}
	input.HandoffTeamID = team.ID
	input.TeamIDs = []string{uuid.NewV7().String()}
	if _, err := update.Execute(ctx, f.owner, created.ID, input); fieldCode(err, "teamIds") != agentaction.ValidationTeamInvalid {
		t.Fatalf("不存在的所属团队应报在所属团队字段：%v", err)
	}
	input.TeamIDs = []string{team.ID}
	updated, err := update.Execute(ctx, f.owner, created.ID, input)
	if err != nil {
		t.Fatalf("保存 AI 员工失败：%v", err)
	}
	if !slices.Equal(updated.ServiceAudiences, []domain.ServiceAudience{domain.ServiceAudienceEmployee}) || updated.HandoffTeamID == nil || *updated.HandoffTeamID != team.ID {
		t.Fatalf("updated agent = %+v", updated)
	}
	if listedAsAssignee(created.IdentityID) {
		t.Fatal("只服务员工的 AI 员工不应接待客户会话")
	}

	if err := teamaction.NewDeleteTeamAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, team.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := agentaction.NewGetAgentQuery(f.db).Execute(ctx, f.owner, created.ID)
	if err != nil || reloaded.HandoffTeamID != nil {
		t.Fatalf("删除团队后转人工应进入公共队列：%+v, %v", reloaded, err)
	}
}
