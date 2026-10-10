//go:build server

package integrationtest

import (
	"context"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestWebsiteContactProfile 验证网站签名身份带入的字段与标签：覆盖客服和 AI 写入的值，跳过未定义字段、非法取值和不存在的标签，客服不能修改、AI 不会覆盖网站同步的值，取值不变时不推进联系人，null 只撤销网站同步的值，标签按网站给出的完整集合增删，并发的客服编辑等待网站同步提交后被拒绝。
func TestWebsiteContactProfile(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	scheduler := agentrunaction.NewScheduler(testEnqueuer)
	visitorService := direct.NewWebsiteVisitorBackend(f.db, scheduler, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	application := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := api.ClientOriginMiddleware("", "CF-IPCountry")(api.NewService(application, api.WithWebsiteVisitor(visitorService, func() bool { return false })))
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	createField := contactprofileaction.NewCreateFieldAction(f.db)
	plan, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{
		Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}},
	})
	require.NoError(t, err)
	seats, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
	require.NoError(t, err)
	company, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeText})
	require.NoError(t, err)
	city, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "城市", Type: domain.ContactFieldTypeText, AIInstruction: "客户所在城市"})
	require.NoError(t, err)
	createTag := contactprofileaction.NewCreateTagAction(f.db)
	vip, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "VIP"})
	require.NoError(t, err)
	returning, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "老客户"})
	require.NoError(t, err)
	manual, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "重点跟进"})
	require.NoError(t, err)
	userID := "user-" + uuid.NewV7().String()
	messagesPath := "/public/website-channels/" + f.channelID + "/messages"
	// 以带给定附加声明的签名身份发送一条消息，首条消息新建会话，之后的消息进入同一会话，返回会话编号。
	sentConversationID := ""
	send := func(extra jwt.MapClaims) string {
		claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix()}
		for key, value := range extra {
			claims[key] = value
		}
		target := "null"
		if sentConversationID != "" {
			target = `"` + sentConversationID + `"`
		}
		body := `{"clientMessageId":"` + uuid.NewV7().String() + `","body":"你好","replyToMessageId":"","conversationId":` + target + `}`
		status, result, _ := customerRequest(t, service, http.MethodPost, messagesPath, signCustomer(t, secret, claims), body)
		require.Equal(t, http.StatusOK, status, "登录用户发送 payload=%+v", result)
		sentConversationID = result["conversation"].(map[string]any)["id"].(string)
		return sentConversationID
	}
	// 读取联系人档案，字段按字段编号、标签按标签编号索引。
	load := func(contactID string) (map[string]contactprofileaction.FieldValue, map[string]contactprofileaction.AssignedTag) {
		profile, err := contactprofileaction.Load(ctx, f.db, f.owner.Workspace.ID, contactID)
		require.NoError(t, err)
		return arr.KeyBy(profile.Fields, func(value contactprofileaction.FieldValue) string { return value.FieldID }),
			arr.KeyBy(profile.Tags, func(tag contactprofileaction.AssignedTag) string { return tag.ID })
	}

	// 首条消息不带档案，客服和 AI 先写入取值与标签。
	conversationID := send(nil)
	var contactID string
	require.NoError(t, f.db.NewSelect().TableExpr("contacts").ColumnExpr("id::text").
		Where("workspace_id = ? AND external_user_id = ?", f.owner.Workspace.ID, userID).Scan(ctx, &contactID))
	require.NoError(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"))
	require.NoError(t, contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, returning.ID))
	require.NoError(t, contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, manual.ID))
	var sessionID string
	require.NoError(t, f.db.NewSelect().TableExpr("service_sessions").ColumnExpr("id::text").
		Where("workspace_id = ? AND conversation_id = ?", f.owner.Workspace.ID, conversationID).Limit(1).Scan(ctx, &sessionID))
	// 以 AI 身份依据指定周期写入城市。
	applyAI := func(value string) {
		require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			_, err := contactprofileaction.ApplyExtraction(ctx, tx, f.owner.Workspace.ID, contactID, sessionID, time.Now(), contactprofileaction.Extraction{Fields: map[string]string{city.ID: value}})
			return err
		}))
	}
	applyAI("北京")
	fields, _ := load(contactID)
	require.Equal(t, domain.ContactProfileSourceAI, fields[city.ID].Source, "网站同步前 fields=%+v", fields)
	require.Equal(t, domain.ContactProfileSourceMember, fields[company.ID].Source, "网站同步前 fields=%+v", fields)

	// 网站带入的取值覆盖客服与 AI，未定义字段、非法取值和不存在的标签跳过，已有标签改由网站维护。
	profileClaims := jwt.MapClaims{
		"attributes": map[string]any{"套餐": "专业版", "席位数": 12, "公司": "Acme", "城市": "上海", "未定义": "x"},
		"tags":       []any{"vip", "老客户", "不存在"},
	}
	send(profileClaims)
	fields, tags := load(contactID)
	want := map[string]string{plan.ID: plan.Options[1].ID, seats.ID: "12", company.ID: "Acme", city.ID: "上海"}
	require.Len(t, fields, len(want), "网站同步后 fields=%+v", fields)
	for fieldID, value := range want {
		got := fields[fieldID]
		require.Equal(t, value, got.Value, "字段 %s", fieldID)
		require.Equal(t, domain.ContactProfileSourceSignedIdentity, got.Source, "字段 %s", fieldID)
		require.Nil(t, got.SourceSession, "字段 %s", fieldID)
	}
	require.Len(t, tags, 3, "网站同步后 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, tags[vip.ID].Source, "网站同步后 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, tags[returning.ID].Source, "网站同步后 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceMember, tags[manual.ID].Source, "网站同步后 tags=%+v", tags)
	agentProfile, err := contactprofileaction.LoadAgentProfile(ctx, f.db, f.owner.Workspace.ID, contactID)
	require.NoError(t, err)
	require.Equal(t, "专业版", agentProfile.Fields["套餐"], "AI 档案 = %+v", agentProfile)
	require.Equal(t, "12", agentProfile.Fields["席位数"], "AI 档案 = %+v", agentProfile)

	// 非法取值不改动已同步的取值。
	send(jwt.MapClaims{"attributes": map[string]any{"套餐": "企业版", "席位数": "很多"}})
	fields, _ = load(contactID)
	require.Equal(t, plan.Options[1].ID, fields[plan.ID].Value, "非法取值后 fields=%+v", fields)
	require.Equal(t, "12", fields[seats.ID].Value, "非法取值后 fields=%+v", fields)

	// 客服不能修改网站同步的取值与标签，AI 也不覆盖。
	require.ErrorIs(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"), contactprofileaction.ErrSyncedFromSignedIdentity, "客服修改网站取值")
	require.ErrorIs(t, contactprofileaction.NewRemoveTagAction(f.db).Execute(ctx, f.member, contactID, vip.ID), contactprofileaction.ErrSyncedFromSignedIdentity, "客服移除网站标签")
	applyAI("广州")
	fields, _ = load(contactID)
	require.Equal(t, "上海", fields[city.ID].Value, "AI 覆盖网站取值 fields=%+v", fields)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, fields[city.ID].Source, "AI 覆盖网站取值 fields=%+v", fields)

	// 取值与标签均未变化时不推进联系人。
	var before, after time.Time
	require.NoError(t, f.db.NewSelect().TableExpr("contacts").Column("updated_at").Where("id = ?", contactID).Scan(ctx, &before))
	send(profileClaims)
	require.NoError(t, f.db.NewSelect().TableExpr("contacts").Column("updated_at").Where("id = ?", contactID).Scan(ctx, &after))
	require.True(t, after.Equal(before), "未变化时联系人更新时间 %s -> %s", before, after)

	// null 清除网站写入的取值；标签集合中缺少的网站标签被移除，客服添加的标签保留。
	send(jwt.MapClaims{"attributes": map[string]any{"公司": nil}, "tags": []any{"VIP"}})
	fields, tags = load(contactID)
	require.NotContains(t, fields, company.ID, "清除后 fields=%+v", fields)
	require.Equal(t, plan.Options[1].ID, fields[plan.ID].Value, "清除后 fields=%+v", fields)
	require.Len(t, tags, 2, "移除后 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, tags[vip.ID].Source, "移除后 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceMember, tags[manual.ID].Source, "移除后 tags=%+v", tags)

	// 未提供标签时不改动标签，空数组移除全部网站标签。
	send(nil)
	_, tags = load(contactID)
	require.Len(t, tags, 2, "未提供标签 tags=%+v", tags)
	send(jwt.MapClaims{"tags": []any{}})
	_, tags = load(contactID)
	require.Len(t, tags, 1, "空标签 tags=%+v", tags)
	require.Equal(t, domain.ContactProfileSourceMember, tags[manual.ID].Source, "空标签 tags=%+v", tags)

	// null 只撤销网站同步的取值，客服填写的取值保留。
	require.NoError(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Other"))
	send(jwt.MapClaims{"attributes": map[string]any{"公司": nil}})
	fields, _ = load(contactID)
	require.Equal(t, "Other", fields[company.ID].Value, "null 后客服取值 fields=%+v", fields)
	require.Equal(t, domain.ContactProfileSourceMember, fields[company.ID].Source, "null 后客服取值 fields=%+v", fields)

	// 网站同步事务未提交时，客服编辑等待其提交后按网站来源被拒绝。
	applied, release := make(chan struct{}), make(chan struct{})
	websiteDone, memberDone := make(chan error, 1), make(chan error, 2)
	go func() {
		websiteDone <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			err := contactprofileaction.ApplySignedProfile(ctx, tx, f.owner.Workspace.ID, contactID, domain.SignedContactProfile{
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
	require.NoError(t, <-websiteDone)
	for range 2 {
		require.ErrorIs(t, <-memberDone, contactprofileaction.ErrSyncedFromSignedIdentity, "并发客服编辑")
	}
	fields, tags = load(contactID)
	require.Equal(t, "Acme", fields[company.ID].Value, "并发后 fields=%+v", fields)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, fields[company.ID].Source, "并发后 fields=%+v", fields)
	require.Equal(t, domain.ContactProfileSourceSignedIdentity, tags[vip.ID].Source, "并发后 tags=%+v", tags)
}
