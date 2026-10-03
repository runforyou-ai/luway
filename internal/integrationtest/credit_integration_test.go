//go:build server

package integrationtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestCredits 验证平台模型调用按积分计费：未定价的模型不可用，每日赠送按需发放一次，调用前按预估预占、结束后多退少补并最多扣到 0，失败且没有用量时全额退回，平台管理员手动加减积分，流水按业务事件汇总并推导过期。
func TestCredits(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL}, nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	admin, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		WorkspaceName: "积分", WorkspaceSlug: "credits", DisplayName: "管理员", Email: "admin@example.test",
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
	organizationID := owner.Organization.ID
	adminMeta.WorkspaceID = organizationID
	memberEmail := uniqueEmail("member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, organizationID, memberEmail, "password123")
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: organizationID, Locale: appservice.LocaleChineseSimplified}

	provider, err := backend.CreatePlatformAIProvider(ctx, adminMeta, appservice.PlatformAIProviderInput{
		Brand: appservice.AIProviderBrandOpenRouter, Name: "平台来源", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "platform-key", APIURL: "https://platform.example.com/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	modelInput := func(name, identifier string, price *appservice.CreditPrice) appservice.PlatformAIModelInput {
		return appservice.PlatformAIModelInput{
			Name: name, Type: appservice.AIModelTypeChat, InputModalities: []appservice.AIModelInputModality{appservice.AIModelInputModalityText},
			ContextWindow: 8192, MaxOutputTokens: 100, Price: price,
			Routes: []appservice.PlatformAIModelRouteInput{{ProviderID: provider.ID, Identifier: identifier, Enabled: true}},
		}
	}

	// 价格校验，未定价的平台模型不出现在选项中，也不能解析调用。
	_, err = backend.CreatePlatformAIModel(ctx, adminMeta, modelInput("负价格", "negative", &appservice.CreditPrice{Input: -1}))
	requireFieldError(t, err, "price", i18n.FieldPlatformAIModelPriceInvalid)
	unpriced, err := backend.CreatePlatformAIModel(ctx, adminMeta, modelInput("未定价", "unpriced", nil))
	if err != nil || unpriced.Price != nil {
		t.Fatalf("unpriced = %#v, err = %v", unpriced, err)
	}
	if _, err := aimodel.Resolve(ctx, db, organizationID, unpriced.ID, domain.AIModelUsageAgent); !errors.Is(err, aimodel.ErrUnavailable) {
		t.Fatalf("resolve unpriced err = %v", err)
	}
	price := &appservice.CreditPrice{Input: 1_000_000, Output: 2_000_000, Request: 3}
	priced, err := backend.CreatePlatformAIModel(ctx, adminMeta, modelInput("计费模型", "priced", price))
	if err != nil || priced.Price == nil || *priced.Price != *price {
		t.Fatalf("priced = %#v, err = %v", priced, err)
	}
	options, err := backend.ListAIModelOptions(ctx, memberMeta, appservice.AIModelUsageAgent)
	if err != nil || len(options.Models) != 1 || options.Models[0].ID != priced.ID || options.Models[0].Price == nil || *options.Models[0].Price != *price {
		t.Fatalf("options = %#v, err = %v", options, err)
	}

	resolved, err := aimodel.Resolve(ctx, db, organizationID, priced.ID, domain.AIModelUsageAgent)
	if err != nil {
		t.Fatal(err)
	}
	scope := modelcall.MemberScope(member.Identity, domain.AIModelCallSourceConversation, "")
	generate := func(invoker *modelcall.Invoker, options agentruntime.ModelOptions, input ...*schema.AgenticMessage) error {
		chat, err := invoker.ChatModels(scope, resolved)(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		_, err = chat.Generate(ctx, input)
		return err
	}
	greeting := schema.UserAgenticMessage("你好")
	invoker := modelcall.New(db, fakeUpstreams(nil, nil))
	balance := func(want int64) appservice.CreditBalance {
		t.Helper()
		got, err := backend.GetCreditBalance(ctx, memberMeta)
		if err != nil || got.Available != want {
			t.Fatalf("balance = %#v, err = %v, want available %d", got, err, want)
		}
		return got
	}

	// 未设置每日赠送时余额为 0，平台模型调用因积分不足被拒绝且不写调用记录。
	balance(0)
	if err := generate(invoker, agentruntime.ModelOptions{}, greeting); !errors.Is(err, credit.ErrInsufficient) {
		t.Fatalf("generate without credits err = %v", err)
	}
	if calls, _ := loadModelCalls(t, db, priced.ID); len(calls) != 0 {
		t.Fatalf("calls without credits = %#v", calls)
	}

	// 每日赠送只能由平台管理员设置，按平台时区当天结束时过期，重复读取不重复发放。
	_, err = backend.UpdatePlatformDailyCreditGrant(ctx, memberMeta, appservice.PlatformDailyCreditGrantInput{DailyCreditGrant: 1000})
	requireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)
	_, err = backend.UpdatePlatformDailyCreditGrant(ctx, adminMeta, appservice.PlatformDailyCreditGrantInput{DailyCreditGrant: -1})
	requireFieldError(t, err, "dailyCreditGrant", i18n.FieldDailyCreditGrantInvalid)
	settings, err := backend.UpdatePlatformDailyCreditGrant(ctx, adminMeta, appservice.PlatformDailyCreditGrantInput{DailyCreditGrant: 1000})
	if err != nil || settings.DailyCreditGrant != 1000 {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
	granted := balance(1000)
	location, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(location)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location)
	if granted.DailyGrant != 1000 || granted.DailyGrantRemaining != 1000 || granted.DailyGrantExpiresAt == nil || !granted.DailyGrantExpiresAt.Equal(tomorrow) {
		t.Fatalf("granted = %#v, want expires at %s", granted, tomorrow)
	}
	balance(1000)

	// 成功调用按实际用量结算：每次 3 积分，输入 10 Token 每个 1 积分，输出 5 Token 每个 2 积分。
	if err := generate(invoker, agentruntime.ModelOptions{}, greeting); err != nil {
		t.Fatal(err)
	}
	calls, _ := loadModelCalls(t, db, priced.ID)
	if len(calls) != 1 || calls[0].Credits != 23 || calls[0].CreditShortfall != 0 || calls[0].RequestCreditPrice == nil || *calls[0].RequestCreditPrice != 3 {
		t.Fatalf("calls = %#v", calls)
	}
	balance(977)

	// 所有来源失败且没有用量时预占全额退回，调用不计入流水。
	failing := modelcall.New(db, fakeUpstreams(map[string]bool{"priced": true}, nil))
	if err := generate(failing, agentruntime.ModelOptions{}, greeting); err == nil {
		t.Fatal("failing upstream succeeded")
	}
	if failed := waitModelCallFinished(t, db, priced.ID); failed.Credits != 0 || failed.Status != string(domain.AIModelCallStatusFailed) {
		t.Fatalf("failed call = %#v", failed)
	}
	balance(977)

	// 平台管理员调整积分：校验积分与备注，扣减按先过期先扣并最多扣到 0。
	_, err = backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 0, Note: " "})
	requireFieldError(t, err, "amount", i18n.FieldCreditAmountInvalid)
	requireFieldError(t, err, "note", i18n.FieldCreditNoteInvalid)
	_, err = backend.AdjustPlatformWorkspaceCredits(ctx, memberMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 10, Note: "补偿"})
	requireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)
	added, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 500, Note: " 补偿 "})
	if err != nil || added.Amount != 500 || added.Balance.Available != 1477 {
		t.Fatalf("added = %#v, err = %v", added, err)
	}
	deducted, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: -1000, Note: "回收"})
	if err != nil || deducted.Amount != -1000 || deducted.Balance.Available != 477 || deducted.Balance.DailyGrantRemaining != 0 {
		t.Fatalf("deducted = %#v, err = %v", deducted, err)
	}
	cleared, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: -5000, Note: "清零"})
	if err != nil || cleared.Amount != -477 || cleared.Balance.Available != 0 {
		t.Fatalf("cleared = %#v, err = %v", cleared, err)
	}
	_, err = backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: -1, Note: "再次扣减"})
	requireLocalizedError(t, err, i18n.ErrorPlatformCreditNothingToDeduct)

	// 实际费用超出预占时从余额补扣，最多扣到 0，不足部分记为差额：空输入预估 1 Token、最大输出 1 Token，预占 3+1+2=6，实际 23，余额 10。
	if _, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 10, Note: "试用"}); err != nil {
		t.Fatal(err)
	}
	if err := generate(invoker, agentruntime.ModelOptions{MaxOutputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if over := waitModelCallFinished(t, db, priced.ID); over.Credits != 10 || over.CreditShortfall != 13 {
		t.Fatalf("over call = %#v", over)
	}
	balance(0)

	// 过期批次的剩余积分不计入余额，在流水中推导为过期。
	if _, err := db.NewInsert().Model(&servermodels.CreditLot{
		CreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, location), OrganizationID: organizationID, Source: string(domain.CreditLotSourceDailyGrant),
		GrantDate: new("2020-01-01"), Amount: 50, Remaining: 50, ExpiresAt: new(time.Date(2020, 1, 2, 0, 0, 0, 0, location)),
	}).Column("created_at", "organization_id", "source", "grant_date", "amount", "remaining", "expires_at").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	balance(0)
	entries, err := backend.ListCreditEntries(ctx, memberMeta, appservice.CreditEntryListInput{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []appservice.CreditEntryKind
	var sum int64
	for _, entry := range entries.Entries {
		kinds = append(kinds, entry.Kind)
		sum += entry.Amount
	}
	want := []appservice.CreditEntryKind{
		appservice.CreditEntryKindModelCall, appservice.CreditEntryKindAdjustment, appservice.CreditEntryKindAdjustment,
		appservice.CreditEntryKindAdjustment, appservice.CreditEntryKindAdjustment, appservice.CreditEntryKindModelCall,
		appservice.CreditEntryKindDailyGrant, appservice.CreditEntryKindExpiration, appservice.CreditEntryKindDailyGrant,
	}
	if entries.Page.Total != len(want) || len(kinds) != len(want) || sum != 0 {
		t.Fatalf("entries = %#v, sum = %d", entries, sum)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("entry kinds = %v, want %v", kinds, want)
		}
	}
	if call := entries.Entries[5]; call.Amount != -23 || call.ModelName != "计费模型" || call.InputTokens != 10 || call.OutputTokens != 5 ||
		call.CallStatus != appservice.AIModelCallStatusSucceeded {
		t.Fatalf("call entry = %#v", call)
	}
	if note := entries.Entries[4]; note.Amount != 500 || note.Note != "补偿" {
		t.Fatalf("adjustment entry = %#v", note)
	}

	// 平台管理员看到与工作区一致的余额与流水，调用记录带扣除积分。
	platformBalance, err := backend.GetPlatformWorkspaceCredits(ctx, adminMeta, organizationID)
	if err != nil || platformBalance.Available != 0 {
		t.Fatalf("platform balance = %#v, err = %v", platformBalance, err)
	}
	platformEntries, err := backend.ListPlatformWorkspaceCreditEntries(ctx, adminMeta, organizationID, appservice.CreditEntryListInput{Page: 1, PageSize: 50})
	if err != nil || platformEntries.Page.Total != len(want) {
		t.Fatalf("platform entries = %#v, err = %v", platformEntries, err)
	}
	_, err = backend.GetPlatformWorkspaceCredits(ctx, adminMeta, "00000000-0000-0000-0000-000000000000")
	requireLocalizedError(t, err, i18n.ErrorPlatformWorkspaceNotFound)
	platformCalls, err := backend.ListPlatformAIModelCalls(ctx, adminMeta, appservice.PlatformAIModelCallListInput{Status: appservice.AIModelCallStatusSucceeded, Page: 1, PageSize: 50})
	if err != nil || len(platformCalls.Calls) != 2 || platformCalls.Calls[0].Credits != 10 || platformCalls.Calls[0].CreditShortfall != 13 || platformCalls.Calls[1].Credits != 23 {
		t.Fatalf("platform calls = %#v, err = %v", platformCalls, err)
	}

	// 并发预占在批次锁内串行，合计不超过余额。
	if _, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 1000, Note: "并发"}); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	reserved := 0
	for range 10 {
		group.Go(func() {
			err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				var callID string
				if err := tx.NewRaw("SELECT uuidv7()::text").Scan(ctx, &callID); err != nil {
					return err
				}
				return credit.Reserve(ctx, tx, organizationID, callID, 300, time.Now())
			})
			if err == nil {
				mu.Lock()
				reserved++
				mu.Unlock()
			} else if !errors.Is(err, credit.ErrInsufficient) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if reserved != 3 {
		t.Fatalf("reserved = %d, want 3", reserved)
	}
	balance(100)
	if _, err := backend.AdjustPlatformWorkspaceCredits(ctx, adminMeta, organizationID, appservice.PlatformCreditAdjustmentInput{Amount: 900, Note: "中断"}); err != nil {
		t.Fatal(err)
	}

	// 开始超过一小时仍在进行中的调用视为中断：按已记录的上游用量结算，多余的预占退回。
	interrupted := &servermodels.AIModelCall{
		CreatedAt: time.Now().Add(-2 * time.Hour), OrganizationID: organizationID, ModelID: priced.ID, ModelName: priced.Name,
		ModelUsage: string(domain.AIModelUsageAgent), ModelScope: string(domain.AIModelScopePlatform),
		ActorType: string(domain.AIModelCallActorSystem), SourceType: string(domain.AIModelCallSourceConversation),
		Status: string(domain.AIModelCallStatusRunning), InputCreditPrice: &price.Input, OutputCreditPrice: &price.Output,
		RequestCreditPrice: &price.Request, Credits: 300,
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(interrupted).
			Column("created_at", "organization_id", "model_id", "model_name", "model_usage", "model_scope", "actor_type", "source_type", "status",
				"input_credit_price", "output_credit_price", "request_credit_price", "credits").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&servermodels.AIModelCallAttempt{
			CallID: interrupted.ID, RouteID: priced.Routes[0].ID, ProviderID: provider.ID, ProviderName: provider.Name, Identifier: "priced",
			Status: string(domain.AIModelCallStatusRunning), InputTokens: 10, OutputTokens: 5,
		}).Column("call_id", "route_id", "provider_id", "provider_name", "identifier", "status", "input_tokens", "output_tokens").Exec(ctx); err != nil {
			return err
		}
		return credit.Reserve(ctx, tx, organizationID, interrupted.ID, 300, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	balance(700)
	if err := modelcall.NewSweepInterruptedAction(db).Execute(ctx, struct{}{}); err != nil {
		t.Fatal(err)
	}
	sweptCall := &servermodels.AIModelCall{}
	if err := db.NewSelect().Model(sweptCall).Where("amc.id = ?", interrupted.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if sweptCall.Status != string(domain.AIModelCallStatusFailed) || sweptCall.Credits != 23 || sweptCall.OutputTokens != 5 || sweptCall.FinishedAt == nil {
		t.Fatalf("swept call = %#v", sweptCall)
	}
	balance(977)
}
