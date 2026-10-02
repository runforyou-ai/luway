//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestServiceDeskDirectory 验证 AI 员工负责人的保存与校验，以及同事目录中服务台的范围、排序、检索和负责人展示。
func TestServiceDeskDirectory(t *testing.T) {
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
		ModelID: aiModelID(t, f.db, provider.ID, "chat-a"), SystemInstruction: "回答同事的问题",
	}}
	createAgent := func(name string, audiences ...domain.ServiceAudience) *agentaction.Agent {
		agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{DisplayName: name, ServiceAudiences: audiences, Execution: execution})
		if err != nil {
			t.Fatalf("创建 AI 员工失败：%v", err)
		}
		return agent
	}
	desk := createAgent("IT 服务台", domain.ServiceAudienceEmployee)
	createAgent("官网客服", domain.ServiceAudienceCustomer)
	listColleagues := func(query string) memberaction.ListColleaguesOutput {
		output, err := memberaction.NewListColleaguesQuery(f.db).Execute(ctx, f.member, memberaction.ListColleaguesInput{Query: query, Page: 1, PageSize: 50})
		if err != nil {
			t.Fatalf("读取同事目录失败：%v", err)
		}
		return output
	}

	update := agentaction.NewUpdateAgentAction(f.db, testServiceSessionReturner(f.db))
	input := agentaction.UpdateInput{
		DisplayName: desk.DisplayName, TeamIDs: []string{}, WorkStatus: domain.WorkStatusWorking,
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, ResponsibleUserID: uuid.NewV7().String(),
	}
	var fieldError *common.FieldError
	if _, err := update.Execute(ctx, f.owner, desk.ID, input); !errors.As(err, &fieldError) || fieldError.Fields["responsibleUserId"] != agentaction.ValidationResponsibleInvalid {
		t.Fatalf("不存在的负责人应被拒绝：%v", err)
	}
	input.ResponsibleUserID = f.member.User.ID
	updated, err := update.Execute(ctx, f.owner, desk.ID, input)
	if err != nil {
		t.Fatalf("保存负责人失败：%v", err)
	}
	if updated.Responsible == nil || updated.Responsible.UserID != f.member.User.ID || updated.Responsible.DisplayName != "成员" || updated.Responsible.Status != domain.IdentityStatusActive {
		t.Fatalf("responsible = %+v", updated.Responsible)
	}

	// 目录只含在职成员与服务员工的 AI 员工，服务台排在最前并带负责人姓名。
	listed := listColleagues("")
	if listed.Page.Total != 3 || len(listed.Colleagues) != 3 {
		t.Fatalf("colleagues = %+v", listed)
	}
	first := listed.Colleagues[0]
	if first.IdentityType != domain.OrganizationIdentityTypeAgent || first.AgentID != desk.ID || first.ResponsibleName != "成员" || first.UserID != "" {
		t.Fatalf("first colleague = %+v", first)
	}
	for _, colleague := range listed.Colleagues[1:] {
		if colleague.IdentityType != domain.OrganizationIdentityTypeUser || colleague.UserID == "" || colleague.Email == "" {
			t.Fatalf("member colleague = %+v", colleague)
		}
	}
	if searched := listColleagues("服务台"); len(searched.Colleagues) != 1 || searched.Colleagues[0].AgentID != desk.ID {
		t.Fatalf("searched = %+v", searched)
	}

	// 负责人停用后目录不再展示其姓名，保留原负责人时仍可保存其他资料。
	if _, err := testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive); err != nil {
		t.Fatalf("停用负责人失败：%v", err)
	}
	listed, err = memberaction.NewListColleaguesQuery(f.db).Execute(ctx, f.owner, memberaction.ListColleaguesInput{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Page.Total != 2 || listed.Colleagues[0].AgentID != desk.ID || listed.Colleagues[0].ResponsibleName != "" {
		t.Fatalf("colleagues after deactivation = %+v", listed)
	}
	input.DisplayName = "IT 服务台 2"
	if updated, err = update.Execute(ctx, f.owner, desk.ID, input); err != nil || updated.Responsible == nil || updated.Responsible.UserID != f.member.User.ID || updated.Responsible.Status != domain.IdentityStatusInactive {
		t.Fatalf("保留已停用负责人时应可保存：%+v, %v", updated, err)
	}
	input.ResponsibleUserID = ""
	if updated, err = update.Execute(ctx, f.owner, desk.ID, input); err != nil || updated.Responsible != nil {
		t.Fatalf("清空负责人失败：%+v, %v", updated, err)
	}
}
