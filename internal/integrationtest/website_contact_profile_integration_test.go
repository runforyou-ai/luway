//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/uptrace/bun"
)

// TestWebsiteContactProfile 验证网站签名身份带入的字段与标签：覆盖客服和 AI 写入的值，跳过未定义字段、非法取值和不存在的标签，客服不能修改、AI 不会覆盖网站同步的值，取值不变时不推进联系人，null 只撤销网站同步的值，标签按网站给出的完整集合增删，并发的客服编辑等待网站同步提交后被拒绝。
func TestWebsiteContactProfile(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	scheduler := agentrunaction.NewScheduler(newTestTasks(f.db))
	visitorService := appservice.NewWebsiteVisitorService(direct.NewWebsiteVisitorBackend(f.db, scheduler, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil))
	application := appservice.New(direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil))
	service := api.NewService(application, api.WithWebsiteVisitor(visitorService, false, "CF-IPCountry"))
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	createField := contactprofileaction.NewCreateFieldAction(f.db)
	plan, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{
		Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	seats, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	company, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeText})
	if err != nil {
		t.Fatal(err)
	}
	city, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "城市", Type: domain.ContactFieldTypeText, AIInstruction: "客户所在城市"})
	if err != nil {
		t.Fatal(err)
	}
	createTag := contactprofileaction.NewCreateTagAction(f.db)
	vip, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "VIP"})
	if err != nil {
		t.Fatal(err)
	}
	returning, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "老客户"})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "重点跟进"})
	if err != nil {
		t.Fatal(err)
	}
	userID := "user-" + uuid.NewV7().String()
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	// 以带给定附加声明的签名身份发送一条新会话消息，返回会话编号。
	send := func(extra jwt.MapClaims) string {
		claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix()}
		for key, value := range extra {
			claims[key] = value
		}
		body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"你好","replyToMessageId":"","conversationId":null}`
		status, result, _ := customerRequest(t, service, http.MethodPost, messagesPath, signCustomer(t, secret, claims), body)
		if status != http.StatusOK {
			t.Fatalf("登录用户发送 status=%d payload=%+v", status, result)
		}
		return result["conversation"].(map[string]any)["id"].(string)
	}
	// 读取联系人档案，字段按字段编号、标签按标签编号索引。
	load := func(contactID string) (map[string]contactprofileaction.FieldValue, map[string]contactprofileaction.AssignedTag) {
		profile, err := contactprofileaction.Load(ctx, f.db, f.owner.Organization.ID, contactID)
		if err != nil {
			t.Fatal(err)
		}
		fields := make(map[string]contactprofileaction.FieldValue, len(profile.Fields))
		for _, value := range profile.Fields {
			fields[value.FieldID] = value
		}
		tags := make(map[string]contactprofileaction.AssignedTag, len(profile.Tags))
		for _, tag := range profile.Tags {
			tags[tag.ID] = tag
		}
		return fields, tags
	}

	// 首条消息不带档案，客服和 AI 先写入取值与标签。
	conversationID := send(nil)
	var contactID string
	if err := f.db.NewSelect().TableExpr("contacts").ColumnExpr("id::text").
		Where("organization_id = ? AND external_user_id = ?", f.owner.Organization.ID, userID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"); err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, returning.ID); err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, manual.ID); err != nil {
		t.Fatal(err)
	}
	var sessionID string
	if err := f.db.NewSelect().TableExpr("service_sessions").ColumnExpr("id::text").
		Where("organization_id = ? AND conversation_id = ?", f.owner.Organization.ID, conversationID).Limit(1).Scan(ctx, &sessionID); err != nil {
		t.Fatal(err)
	}
	// 以 AI 身份依据指定周期写入城市。
	applyAI := func(value string) {
		if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			_, err := contactprofileaction.ApplyExtraction(ctx, tx, f.owner.Organization.ID, contactID, sessionID, time.Now(), contactprofileaction.Extraction{Fields: map[string]string{city.ID: value}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	applyAI("北京")
	if fields, _ := load(contactID); fields[city.ID].Source != domain.ContactProfileSourceAI || fields[company.ID].Source != domain.ContactProfileSourceMember {
		t.Fatalf("网站同步前 fields=%+v", fields)
	}

	// 网站带入的取值覆盖客服与 AI，未定义字段、非法取值和不存在的标签跳过，已有标签改由网站维护。
	profileClaims := jwt.MapClaims{
		"attributes": map[string]any{"套餐": "专业版", "席位数": 12, "公司": "Acme", "城市": "上海", "未定义": "x"},
		"tags":       []any{"vip", "老客户", "不存在"},
	}
	send(profileClaims)
	fields, tags := load(contactID)
	want := map[string]string{plan.ID: plan.Options[1].ID, seats.ID: "12", company.ID: "Acme", city.ID: "上海"}
	if len(fields) != len(want) {
		t.Fatalf("网站同步后 fields=%+v", fields)
	}
	for fieldID, value := range want {
		if got := fields[fieldID]; got.Value != value || got.Source != domain.ContactProfileSourceSignedIdentity || got.SourceSession != nil {
			t.Fatalf("字段 %s = %+v, want %q", fieldID, got, value)
		}
	}
	if len(tags) != 3 || tags[vip.ID].Source != domain.ContactProfileSourceSignedIdentity || tags[returning.ID].Source != domain.ContactProfileSourceSignedIdentity ||
		tags[manual.ID].Source != domain.ContactProfileSourceMember {
		t.Fatalf("网站同步后 tags=%+v", tags)
	}
	agentProfile, err := contactprofileaction.LoadAgentProfile(ctx, f.db, f.owner.Organization.ID, contactID)
	if err != nil {
		t.Fatal(err)
	}
	if agentProfile.Fields["套餐"] != "专业版" || agentProfile.Fields["席位数"] != "12" {
		t.Fatalf("AI 档案 = %+v", agentProfile)
	}

	// 非法取值不改动已同步的取值。
	send(jwt.MapClaims{"attributes": map[string]any{"套餐": "企业版", "席位数": "很多"}})
	if fields, _ := load(contactID); fields[plan.ID].Value != plan.Options[1].ID || fields[seats.ID].Value != "12" {
		t.Fatalf("非法取值后 fields=%+v", fields)
	}

	// 客服不能修改网站同步的取值与标签，AI 也不覆盖。
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"); !errors.Is(err, contactprofileaction.ErrSyncedFromSignedIdentity) {
		t.Fatalf("客服修改网站取值 err = %v", err)
	}
	if err := contactprofileaction.NewRemoveTagAction(f.db).Execute(ctx, f.member, contactID, vip.ID); !errors.Is(err, contactprofileaction.ErrSyncedFromSignedIdentity) {
		t.Fatalf("客服移除网站标签 err = %v", err)
	}
	applyAI("广州")
	if fields, _ := load(contactID); fields[city.ID].Value != "上海" || fields[city.ID].Source != domain.ContactProfileSourceSignedIdentity {
		t.Fatalf("AI 覆盖网站取值 fields=%+v", fields)
	}

	// 取值与标签均未变化时不推进联系人。
	var before, after time.Time
	if err := f.db.NewSelect().TableExpr("contacts").Column("updated_at").Where("id = ?", contactID).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	send(profileClaims)
	if err := f.db.NewSelect().TableExpr("contacts").Column("updated_at").Where("id = ?", contactID).Scan(ctx, &after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("未变化时联系人更新时间 %s -> %s", before, after)
	}

	// null 清除网站写入的取值；标签集合中缺少的网站标签被移除，客服添加的标签保留。
	send(jwt.MapClaims{"attributes": map[string]any{"公司": nil}, "tags": []any{"VIP"}})
	fields, tags = load(contactID)
	if _, ok := fields[company.ID]; ok || fields[plan.ID].Value != plan.Options[1].ID {
		t.Fatalf("清除后 fields=%+v", fields)
	}
	if len(tags) != 2 || tags[vip.ID].Source != domain.ContactProfileSourceSignedIdentity || tags[manual.ID].Source != domain.ContactProfileSourceMember {
		t.Fatalf("移除后 tags=%+v", tags)
	}

	// 未提供标签时不改动标签，空数组移除全部网站标签。
	send(nil)
	if _, tags := load(contactID); len(tags) != 2 {
		t.Fatalf("未提供标签 tags=%+v", tags)
	}
	send(jwt.MapClaims{"tags": []any{}})
	if _, tags := load(contactID); len(tags) != 1 || tags[manual.ID].Source != domain.ContactProfileSourceMember {
		t.Fatalf("空标签 tags=%+v", tags)
	}

	// null 只撤销网站同步的取值，客服填写的取值保留。
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"); err != nil {
		t.Fatal(err)
	}
	send(jwt.MapClaims{"attributes": map[string]any{"公司": nil}})
	if fields, _ := load(contactID); fields[company.ID].Value != "Other" || fields[company.ID].Source != domain.ContactProfileSourceMember {
		t.Fatalf("null 后客服取值 fields=%+v", fields)
	}

	// 网站同步事务未提交时，客服编辑等待其提交后按网站来源被拒绝。
	applied, release := make(chan struct{}), make(chan struct{})
	websiteDone, memberDone := make(chan error, 1), make(chan error, 2)
	go func() {
		websiteDone <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			err := contactprofileaction.ApplySignedProfile(ctx, tx, f.owner.Organization.ID, contactID, domain.SignedContactProfile{
				Attributes: map[string]string{"公司": "Acme"}, Tags: []string{"VIP"},
			})
			close(applied)
			<-release
			return err
		})
	}()
	<-applied
	go func() {
		memberDone <- contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other")
	}()
	go func() {
		memberDone <- contactprofileaction.NewRemoveTagAction(f.db).Execute(ctx, f.member, contactID, vip.ID)
	}()
	select {
	case err := <-memberDone:
		t.Fatalf("客服编辑未等待网站同步 err=%v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-websiteDone; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-memberDone; !errors.Is(err, contactprofileaction.ErrSyncedFromSignedIdentity) {
			t.Fatalf("并发客服编辑 err = %v", err)
		}
	}
	fields, tags = load(contactID)
	if fields[company.ID].Value != "Acme" || fields[company.ID].Source != domain.ContactProfileSourceSignedIdentity || tags[vip.ID].Source != domain.ContactProfileSourceSignedIdentity {
		t.Fatalf("并发后 fields=%+v tags=%+v", fields, tags)
	}
}
