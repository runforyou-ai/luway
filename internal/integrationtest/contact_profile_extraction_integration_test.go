//go:build server

package integrationtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	contactprofileaction "github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/actions/servicesummary"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/decision"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestContactProfileExtraction 验证周期关闭后 AI 按来源优先级填写字段、联系方式与称呼并按条件打标签：客服填写的值不被覆盖，AI 可以更新自己写的值和客服清空的值，更早关闭的周期不覆盖较新的结果，周期再次关闭后旧任务不再写入。
func TestContactProfileExtraction(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := newTestTasks(f.db)
	coordinator := newGroupAgentCoordinator(f.db)
	providerID := seedSummaryModels(t, f.db, f.owner)
	if _, err := customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "decision-model"},
		Summary:  &domain.AIModelReference{ProviderID: providerID, ModelIdentifier: "chat-model"},
		Locale:   domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}
	createField := contactprofileaction.NewCreateFieldAction(f.db)
	company, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeText, AIInstruction: "客户所在公司"})
	if err != nil {
		t.Fatal(err)
	}
	city, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "城市", Type: domain.ContactFieldTypeText, AIInstruction: " 客户所在城市 "})
	if err != nil {
		t.Fatal(err)
	}
	if city.AIInstruction != "客户所在城市" {
		t.Fatalf("AI 填写说明 = %q", city.AIInstruction)
	}
	plan, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{
		Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}}, AIInstruction: "客户使用的套餐",
	})
	if err != nil {
		t.Fatal(err)
	}
	seats, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	createTag := contactprofileaction.NewCreateTagAction(f.db)
	intent, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "有采购意向", AIInstruction: "客户表示打算购买或升级"})
	if err != nil {
		t.Fatal(err)
	}
	manualTag, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "VIP"})
	if err != nil {
		t.Fatal(err)
	}
	var contactID string
	if err := f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cci.contact_id::text").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id").
		Where("cc.conversation_id = ?", f.conversationID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("contacts").Set("display_name = NULL").Where("id = ?", contactID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Acme"); err != nil {
		t.Fatal(err)
	}

	// 人工关闭后投递资料抽取任务。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	session := loadSummarySession(t, f.db, f.conversationID)
	input := loadExtractContactProfileInput(t, f.db, session.ID)

	decider := &summaryDecider{answers: map[string]decision.Answer{"tag_0": {Kind: decision.KindYesNo, Probability: 0.9}}}
	caller := &summaryCaller{text: `{"name":"王先生","fields":[{"name":"公司","value":"Other"},{"name":"城市","value":"上海"},{"name":"套餐","value":"专业版"},{"name":"席位数","value":"5"}],` +
		`"emails":["Wang@Example.com","not-an-email"],"phones":["+86 138 0000 0000","13800000000"]}`}
	worker := servicesummary.NewWorker(f.db, tasks, decider, caller)
	if err := worker.ExtractContactProfile(ctx, input); err != nil {
		t.Fatal(err)
	}
	// 客服填写的字段和未设置填写说明的字段不提供给模型，已拥有或只能由客服添加的标签不参与判断。
	if strings.Contains(caller.input, "Acme") || strings.Contains(caller.input, "席位数") {
		t.Fatalf("模型资料包含不可填写的字段：%s", caller.input)
	}
	if len(decider.questions) != 1 || !strings.Contains(decider.questions["tag_0"].Instructions, "客户表示打算购买或升级") {
		t.Fatalf("标签判断题 = %+v", decider.questions)
	}
	profile, err := contactprofileaction.Load(ctx, f.db, f.owner.Organization.ID, contactID)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]contactprofileaction.FieldValue, len(profile.Fields))
	for _, value := range profile.Fields {
		values[value.FieldID] = value
	}
	if value := values[company.ID]; value.Value != "Acme" || value.Source != domain.ContactProfileSourceMember || value.SourceSession != nil {
		t.Fatalf("客服填写的公司 = %+v", value)
	}
	if value := values[city.ID]; value.Value != "上海" || value.Source != domain.ContactProfileSourceAI || value.SourceSession == nil ||
		value.SourceSession.ConversationID != f.conversationID || value.SourceSession.OpeningMessageID != session.OpeningMessageID {
		t.Fatalf("AI 填写的城市 = %+v", value)
	}
	if value := values[plan.ID]; value.Value != plan.Options[1].ID || value.Source != domain.ContactProfileSourceAI {
		t.Fatalf("AI 填写的套餐 = %+v", value)
	}
	if _, ok := values[seats.ID]; ok {
		t.Fatal("未设置填写说明的字段被 AI 填写")
	}
	if len(profile.Tags) != 1 || profile.Tags[0].ID != intent.ID || profile.Tags[0].Source != domain.ContactProfileSourceAI || profile.Tags[0].SourceSession == nil {
		t.Fatalf("AI 添加的标签 = %+v", profile.Tags)
	}
	// 模型输出的称呼不写入显示名称。
	var displayName sql.NullString
	if err := f.db.NewSelect().Table("contacts").Column("display_name").Where("id = ?", contactID).Scan(ctx, &displayName); err != nil {
		t.Fatal(err)
	}
	if displayName.Valid {
		t.Fatalf("AI 写入了显示名称 = %q", displayName.String)
	}
	methods := make([]servermodels.ContactMethod, 0)
	if err := f.db.NewSelect().Model(&methods).Where("cm.contact_id = ?", contactID).OrderExpr("cm.type").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0].Value != "wang@example.com" || !methods[0].IsPrimary || methods[1].Value != "+8613800000000" || !methods[1].IsPrimary {
		t.Fatalf("AI 添加的联系方式 = %+v", methods)
	}

	// 客服改写 AI 的值后 AI 不再改动，AI 可以更新自己写的值，客服添加的标签不受影响。
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, city.ID, "北京"); err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, manualTag.ID); err != nil {
		t.Fatal(err)
	}
	caller.text = `{"fields":[{"name":"城市","value":"广州"},{"name":"套餐","value":"基础版"}],"emails":[],"phones":[]}`
	if err := worker.ExtractContactProfile(ctx, input); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(caller.input, "北京") {
		t.Fatalf("模型资料包含客服改写的字段：%s", caller.input)
	}
	profile, err = contactprofileaction.Load(ctx, f.db, f.owner.Organization.ID, contactID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range profile.Fields {
		values[value.FieldID] = value
	}
	if value := values[city.ID]; value.Value != "北京" || value.Source != domain.ContactProfileSourceMember || value.SourceSession != nil {
		t.Fatalf("客服改写后的城市 = %+v", value)
	}
	if value := values[plan.ID]; value.Value != plan.Options[0].ID || value.Source != domain.ContactProfileSourceAI {
		t.Fatalf("AI 更新的套餐 = %+v", value)
	}

	// 客服修改过小结的周期重开再关闭时仍投递抽取任务，上一次关闭的任务不再写入。
	if _, err := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: session.ID, Summary: "客服修改的小结",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	reclosed := loadExtractContactProfileInput(t, f.db, session.ID)
	if reclosed.ClosedAt.Equal(input.ClosedAt) {
		t.Fatal("客服修改过小结的周期再次关闭时没有投递抽取任务")
	}
	callsBefore := caller.calls
	if err := worker.ExtractContactProfile(ctx, input); err != nil {
		t.Fatal(err)
	}
	if caller.calls != callsBefore {
		t.Fatal("周期再次关闭后旧任务仍调用了模型")
	}

	// 客户新开周期后关闭，新周期的结果先写入；更早关闭的周期的任务随后执行时不覆盖，较新周期重开后同样不覆盖。
	age, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "年龄", Type: domain.ContactFieldTypeNumber, AIInstruction: "客户的年龄"})
	if err != nil {
		t.Fatal(err)
	}
	complaint, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "投诉", AIInstruction: "客户表达了投诉"})
	if err != nil {
		t.Fatal(err)
	}
	if err := contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, city.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.visitorMessage(ctx, "我想升级到专业版"); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	next := loadSummarySession(t, f.db, f.conversationID)
	if next.ID == session.ID {
		t.Fatal("客户新消息没有开启新周期")
	}
	// 模型把数字字段输出为 JSON 数字；投诉标签概率不足不添加。
	decider.answers = map[string]decision.Answer{"tag_0": {Kind: decision.KindYesNo, Probability: 0.5}}
	caller.text = `{"name":"","fields":[{"name":"城市","value":"广州"},{"name":"套餐","value":"专业版"},{"name":"年龄","value":30}],"emails":[],"phones":[]}`
	if err := worker.ExtractContactProfile(ctx, loadExtractContactProfileInput(t, f.db, next.ID)); err != nil {
		t.Fatal(err)
	}
	// 较新的周期重开后，更早关闭的周期的任务仍不覆盖其结果。
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	caller.text = `{"name":"","fields":[{"name":"套餐","value":"基础版"}],"emails":[],"phones":[]}`
	if err := worker.ExtractContactProfile(ctx, reclosed); err != nil {
		t.Fatal(err)
	}
	profile, err = contactprofileaction.Load(ctx, f.db, f.owner.Organization.ID, contactID)
	if err != nil {
		t.Fatal(err)
	}
	values = make(map[string]contactprofileaction.FieldValue, len(profile.Fields))
	for _, value := range profile.Fields {
		values[value.FieldID] = value
	}
	if value := values[city.ID]; value.Value != "广州" || value.Source != domain.ContactProfileSourceAI {
		t.Fatalf("客服清空后 AI 重新填写的城市 = %+v", value)
	}
	if value := values[plan.ID]; value.Value != plan.Options[1].ID || value.SourceSession == nil || value.SourceSession.OpeningMessageID != next.OpeningMessageID {
		t.Fatalf("更早关闭的周期覆盖了套餐 = %+v", value)
	}
	if value := values[age.ID]; value.Value != "30" {
		t.Fatalf("数字取值 = %+v", value)
	}
	for _, tag := range profile.Tags {
		if tag.ID == complaint.ID {
			t.Fatal("概率不足的标签被添加")
		}
	}
}

// loadExtractContactProfileInput 读取指定周期最近一次投递的联系人资料抽取任务输入。
func loadExtractContactProfileInput(t *testing.T, db *bun.DB, serviceSessionID string) servicesummary.ExtractContactProfileInput {
	t.Helper()
	var row struct {
		Payload json.RawMessage `bun:"payload"`
	}
	if err := db.NewSelect().TableExpr("task_runs AS tr").Column("tr.payload").
		Where("tr.action_name = ? AND tr.payload->>'serviceSessionId' = ?", servicesummary.ExtractContactProfileActionName, serviceSessionID).
		OrderExpr("tr.created_at DESC, tr.id DESC").Limit(1).
		Scan(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	var input servicesummary.ExtractContactProfileInput
	if err := json.Unmarshal(row.Payload, &input); err != nil {
		t.Fatal(err)
	}
	return input
}
