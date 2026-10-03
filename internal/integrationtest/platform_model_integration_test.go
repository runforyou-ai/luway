//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/uptrace/bun"
)

// TestPlatformModels 验证平台管理员维护平台供应商与多来源平台模型，工作区按模型名称选用并按来源顺序调用，调用记录只对平台管理员展示平台模型调用，被引用的模型与仍是来源的供应商不能删除。
func TestPlatformModels(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL}, nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}
	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "平台模型", WorkspaceSlug: "platform-models", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	owner := resolveMemberSession(t, db, workspaces.Items[0].ID, admin.Token).Identity
	adminMeta.WorkspaceID = owner.Organization.ID
	memberEmail := uniqueEmail("member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: owner.Organization.ID, Locale: appservice.LocaleChineseSimplified}

	// 平台模型服务只对平台管理员开放。
	_, err = backend.ListPlatformAIModels(ctx, memberMeta)
	requireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)

	primary, err := backend.CreatePlatformAIProvider(ctx, adminMeta, appservice.PlatformAIProviderInput{
		Brand: appservice.AIProviderBrandOpenRouter, Name: "主来源", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "primary-key", APIURL: "https://primary.example.com/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := backend.CreatePlatformAIProvider(ctx, adminMeta, appservice.PlatformAIProviderInput{
		Brand: appservice.AIProviderBrandDeepSeek, Name: "备用来源", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "backup-key", APIURL: "https://backup.example.com/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.CreatePlatformAIProvider(ctx, adminMeta, appservice.PlatformAIProviderInput{
		Brand: appservice.AIProviderBrandDeepSeek, Name: "备用来源", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "other-key", APIURL: "https://other.example.com/v1",
	})
	requireFieldError(t, err, "name", i18n.FieldAIProviderNameDuplicate)

	input := appservice.PlatformAIModelInput{
		Name: "DeepSeek V4.1 Flash", Type: appservice.AIModelTypeChat,
		InputModalities: []appservice.AIModelInputModality{appservice.AIModelInputModalityText},
		ContextWindow:   131072, MaxOutputTokens: 8192, Price: &appservice.CreditPrice{},
		Routes: []appservice.PlatformAIModelRouteInput{
			{ProviderID: primary.ID, Identifier: "deepseek/flash", Enabled: true},
			{ProviderID: backup.ID, Identifier: "deepseek-flash", Enabled: true},
		},
	}
	_, err = backend.CreatePlatformAIModel(ctx, adminMeta, appservice.PlatformAIModelInput{
		Name: input.Name, Type: input.Type, InputModalities: input.InputModalities, ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens,
	})
	requireFieldError(t, err, "routes", i18n.FieldPlatformAIModelRoutesInvalid)
	platform, err := backend.CreatePlatformAIModel(ctx, adminMeta, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(platform.Routes) != 2 || platform.Routes[0].ProviderName != "主来源" || platform.Routes[1].Identifier != "deepseek-flash" {
		t.Fatalf("platform model = %#v", platform)
	}
	duplicate := input
	duplicate.Routes = []appservice.PlatformAIModelRouteInput{{ProviderID: backup.ID, Identifier: "another", Enabled: true}}
	_, err = backend.CreatePlatformAIModel(ctx, adminMeta, duplicate)
	requireFieldError(t, err, "name", i18n.FieldPlatformAIModelNameDuplicate)

	// 工作区成员只看到平台模型的名称，排在工作区模型前面，看不到平台供应商。
	workspaceProvider, err := backend.CreateAIProvider(ctx, adminMeta, appservice.AIProviderInput{
		Brand: appservice.AIProviderBrandOpenAI, Name: "工作区供应商", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "workspace-key", APIURL: "https://workspace.example.com/v1",
		Models: []appservice.AIProviderModel{{
			Identifier: "workspace-chat", Name: "工作区模型", Type: appservice.AIModelTypeChat,
			InputModalities: []appservice.AIModelInputModality{appservice.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 1024,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceModelID := workspaceProvider.Models[0].ID
	options, err := backend.ListAIModelOptions(ctx, memberMeta, appservice.AIModelUsageAgent)
	if err != nil || len(options.Models) != 2 {
		t.Fatalf("options = %#v, err = %v", options, err)
	}
	if first := options.Models[0]; first.ID != platform.ID || first.Scope != appservice.AIModelScopePlatform || first.Provider != nil {
		t.Fatalf("platform option = %#v", first)
	}
	if second := options.Models[1]; second.ID != workspaceModelID || second.Scope != appservice.AIModelScopeWorkspace || second.Provider == nil {
		t.Fatalf("workspace option = %#v", second)
	}
	providers, err := backend.ListAIProviders(ctx, memberMeta)
	if err != nil || len(providers.Providers) != 1 {
		t.Fatalf("workspace providers = %#v, err = %v", providers, err)
	}

	// 主来源失败时由备用来源完成，调用记为平台模型调用。
	resolved, err := aimodel.Resolve(ctx, db, owner.Organization.ID, platform.ID, domain.AIModelUsageAgent)
	if err != nil {
		t.Fatal(err)
	}
	invoker := modelcall.New(db, fakeUpstreams(map[string]bool{"deepseek/flash": true}, nil))
	chat, err := invoker.ChatModels(modelcall.MemberScope(member.Identity, domain.AIModelCallSourceConversation, ""), resolved)(ctx, agentruntime.ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Generate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	insertWorkspaceModelCall(t, db, invoker, owner.Organization.ID, workspaceModelID)
	calls, err := backend.ListPlatformAIModelCalls(ctx, adminMeta, appservice.PlatformAIModelCallListInput{Page: 1, PageSize: 50})
	if err != nil || len(calls.Calls) != 1 || calls.Page.Total != 1 {
		t.Fatalf("calls = %#v, err = %v", calls, err)
	}
	call := calls.Calls[0]
	if call.ModelID != platform.ID || call.WorkspaceName != "平台模型" || call.Status != appservice.AIModelCallStatusSucceeded || call.AttemptCount != 2 || call.Actor != appservice.AIModelCallActorMember {
		t.Fatalf("call = %#v", call)
	}
	detail, err := backend.GetPlatformAIModelCall(ctx, adminMeta, call.ID)
	if err != nil || len(detail.Attempts) != 2 ||
		detail.Attempts[0].ProviderName != "主来源" || detail.Attempts[0].Status != appservice.AIModelCallStatusFailed ||
		detail.Attempts[1].ProviderName != "备用来源" || detail.Attempts[1].Status != appservice.AIModelCallStatusSucceeded {
		t.Fatalf("call detail = %#v, err = %v", detail, err)
	}
	filtered, err := backend.ListPlatformAIModelCalls(ctx, adminMeta, appservice.PlatformAIModelCallListInput{Status: appservice.AIModelCallStatusFailed, Page: 1, PageSize: 50})
	if err != nil || len(filtered.Calls) != 0 {
		t.Fatalf("failed calls = %#v, err = %v", filtered, err)
	}

	// 调整来源顺序与启用状态时保留来源编号。
	reordered := input
	reordered.Routes = []appservice.PlatformAIModelRouteInput{
		{ID: platform.Routes[1].ID, ProviderID: backup.ID, Identifier: "deepseek-flash", Enabled: true},
		{ID: platform.Routes[0].ID, ProviderID: primary.ID, Identifier: "deepseek/flash", Enabled: false},
	}
	updated, err := backend.UpdatePlatformAIModel(ctx, adminMeta, platform.ID, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Routes[0].ID != platform.Routes[1].ID || updated.Routes[1].ID != platform.Routes[0].ID || updated.Routes[1].Enabled {
		t.Fatalf("updated routes = %#v", updated.Routes)
	}
	resolved, err = aimodel.Resolve(ctx, db, owner.Organization.ID, platform.ID, domain.AIModelUsageAgent)
	if err != nil || len(resolved.Routes) != 1 || resolved.Routes[0].ProviderID != backup.ID {
		t.Fatalf("resolved = %#v, err = %v", resolved, err)
	}

	// 被 AI 员工引用的平台模型停用全部来源后，已保存配置仍能读出模型，调用与选择时不可用。
	pausedInput := input
	pausedInput.Name = "暂停模型"
	pausedInput.Routes = []appservice.PlatformAIModelRouteInput{{ProviderID: backup.ID, Identifier: "paused-model", Enabled: true}}
	paused, err := backend.CreatePlatformAIModel(ctx, adminMeta, pausedInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{
		DisplayName: "平台模型员工",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: paused.ID, SystemInstruction: "回答问题",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	pausedInput.Routes = []appservice.PlatformAIModelRouteInput{{ID: paused.Routes[0].ID, ProviderID: backup.ID, Identifier: "paused-model", Enabled: false}}
	if _, err := backend.UpdatePlatformAIModel(ctx, adminMeta, paused.ID, pausedInput); err != nil {
		t.Fatal(err)
	}
	agents, err := backend.ListAgents(ctx, memberMeta, appservice.AgentListInput{Page: 1, PageSize: 50})
	if err != nil || len(agents.Agents) != 1 || agents.Agents[0].Execution.Managed == nil ||
		agents.Agents[0].Execution.Managed.Model.Name != "暂停模型" || agents.Agents[0].Execution.Managed.Model.Provider != nil {
		t.Fatalf("agents = %#v, err = %v", agents, err)
	}
	if _, err := aimodel.Resolve(ctx, db, owner.Organization.ID, paused.ID, domain.AIModelUsageAgent); err != aimodel.ErrUnavailable {
		t.Fatalf("resolve paused platform model err = %v", err)
	}
	options, err = backend.ListAIModelOptions(ctx, memberMeta, appservice.AIModelUsageAgent)
	if err != nil || len(options.Models) != 2 || options.Models[0].ID == paused.ID {
		t.Fatalf("options with paused model = %#v, err = %v", options, err)
	}

	// 工作区引用平台模型后，模型不能删除或改成不满足引用用途的类型，仍是来源的供应商不能删除。
	if _, err := customerserviceaction.NewUpdateTranslationSettingsAction(db).Execute(ctx, owner, &platform.ID); err != nil {
		t.Fatal(err)
	}
	err = backend.DeletePlatformAIModel(ctx, adminMeta, platform.ID)
	requireLocalizedError(t, err, i18n.ErrorPlatformAIModelInUse)
	embedding := reordered
	embedding.Type = appservice.AIModelTypeEmbedding
	_, err = backend.UpdatePlatformAIModel(ctx, adminMeta, platform.ID, embedding)
	requireFieldError(t, err, "type", i18n.FieldPlatformAIModelUsageConflict)
	err = backend.DeletePlatformAIProvider(ctx, adminMeta, primary.ID)
	requireLocalizedError(t, err, i18n.ErrorPlatformAIProviderInUse)

	if _, err := customerserviceaction.NewUpdateTranslationSettingsAction(db).Execute(ctx, owner, nil); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeletePlatformAIModel(ctx, adminMeta, platform.ID); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeletePlatformAIProvider(ctx, adminMeta, primary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := aimodel.Resolve(ctx, db, owner.Organization.ID, platform.ID, domain.AIModelUsageAgent); err != aimodel.ErrUnavailable {
		t.Fatalf("resolve deleted platform model err = %v", err)
	}
	remaining, err := backend.ListPlatformAIProviders(ctx, adminMeta)
	if err != nil || len(remaining.Providers) != 1 || remaining.Providers[0].ID != backup.ID || remaining.Providers[0].ModelCount != 1 {
		t.Fatalf("remaining providers = %#v, err = %v", remaining, err)
	}
}

// insertWorkspaceModelCall 以工作区模型发起一次后台调用。
func insertWorkspaceModelCall(t *testing.T, db bun.IDB, invoker *modelcall.Invoker, organizationID, modelID string) {
	t.Helper()
	ctx := context.Background()
	resolved, err := aimodel.Resolve(ctx, db, organizationID, modelID, domain.AIModelUsageAgent)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := invoker.ChatModels(modelcall.SystemScope(organizationID, domain.AIModelCallSourceConversation, ""), resolved)(ctx, agentruntime.ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Generate(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

// requireLocalizedError 断言错误是按简体中文本地化的指定业务错误。
func requireLocalizedError(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	want, _ := i18n.Localize("zh-CN", key)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Message != want {
		t.Fatalf("error = %v, want %s（%s）", err, key, want)
	}
}
