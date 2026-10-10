//go:build server

package integrationtest

import (
	"context"
	"database/sql"
	"testing"

	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// TestContactProfileExtraction 验证周期关闭后 AI 按来源优先级填写字段、联系方式与称呼并按条件打标签：客服填写的值不被覆盖，AI 可以更新自己写的值和客服清空的值，更早关闭的周期不覆盖较新的结果，周期再次关闭后旧任务不再写入。
func TestContactProfileExtraction(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	coordinator := newGroupAgentCoordinator(f.db)
	providerID := seedSummaryModels(t, f.db, f.owner)
	_, err := customerservice.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, f.db, providerID, "decision-model")),
		SummaryModelID:  new(aiModelID(t, f.db, providerID, "chat-model")),
		Locale:          domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)
	createField := contactprofileaction.NewCreateFieldAction(f.db)
	company, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeText, AIInstruction: "客户所在公司"})
	require.NoError(t, err)
	city, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "城市", Type: domain.ContactFieldTypeText, AIInstruction: " 客户所在城市 "})
	require.NoError(t, err)
	require.Equal(t, "客户所在城市", city.AIInstruction, "AI 填写说明")
	plan, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{
		Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}}, AIInstruction: "客户使用的套餐",
	})
	require.NoError(t, err)
	seats, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
	require.NoError(t, err)
	createTag := contactprofileaction.NewCreateTagAction(f.db)
	intent, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "有采购意向", AIInstruction: "客户表示打算购买或升级"})
	require.NoError(t, err)
	manualTag, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "VIP"})
	require.NoError(t, err)
	var contactID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("ci.contact_id::text").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Where("cc.conversation_id = ?", f.conversationID).Scan(ctx, &contactID))
	_, err = f.db.NewUpdate().Table("contacts").Set("display_name = NULL").Where("id = ?", contactID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, company.ID, "Acme"))

	// 人工关闭后投递资料抽取任务。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	session := loadSummarySession(t, f.db, f.conversationID)
	input := loadExtractContactProfileInput(t, session.ID, tasks, testEnqueuer)

	decider := &summaryDecider{answers: map[string]decision.Answer{"tag_0": {Kind: decision.KindYesNo, Probability: 0.9}}}
	caller := &summaryCaller{text: `{"name":"王先生","fields":[{"name":"公司","value":"Other"},{"name":"城市","value":"上海"},{"name":"套餐","value":"专业版"},{"name":"席位数","value":"5"}],` +
		`"emails":["Wang@Example.com","not-an-email"],"phones":["+86 138 0000 0000","13800000000"]}`}
	worker := servicesummary.NewWorker(f.db, tasks, modelcall.New(f.db, modelcall.Upstreams{Decider: decider, Chat: caller.chat}, nil))
	require.NoError(t, worker.ExtractContactProfile(ctx, input))
	// 客服填写的字段和未设置填写说明的字段不提供给模型，已拥有或只能由客服添加的标签不参与判断。
	require.NotContains(t, caller.input, "Acme", "模型资料包含不可填写的字段")
	require.NotContains(t, caller.input, "席位数", "模型资料包含不可填写的字段")
	require.Len(t, decider.questions, 1, "标签判断题")
	require.Contains(t, decider.questions["tag_0"].Instructions, "客户表示打算购买或升级")
	profile, err := contactprofileaction.Load(ctx, f.db, f.owner.Workspace.ID, contactID)
	require.NoError(t, err)
	values := arr.KeyBy(profile.Fields, func(value contactprofileaction.FieldValue) string { return value.FieldID })
	require.Equal(t, "Acme", values[company.ID].Value, "客服填写的公司")
	require.Equal(t, domain.ContactProfileSourceMember, values[company.ID].Source)
	require.Nil(t, values[company.ID].SourceSession)
	require.Equal(t, "上海", values[city.ID].Value, "AI 填写的城市")
	require.Equal(t, domain.ContactProfileSourceAI, values[city.ID].Source)
	require.Equal(t, &contactprofileaction.ProfileSourceSession{ConversationID: f.conversationID, OpeningMessageID: session.OpeningMessageID}, values[city.ID].SourceSession)
	require.Equal(t, plan.Options[1].ID, values[plan.ID].Value, "AI 填写的套餐")
	require.Equal(t, domain.ContactProfileSourceAI, values[plan.ID].Source)
	require.NotContains(t, values, seats.ID, "未设置填写说明的字段被 AI 填写")
	require.Len(t, profile.Tags, 1, "AI 添加的标签")
	require.Equal(t, intent.ID, profile.Tags[0].ID)
	require.Equal(t, domain.ContactProfileSourceAI, profile.Tags[0].Source)
	require.NotNil(t, profile.Tags[0].SourceSession)
	// 模型输出的称呼不写入显示名称。
	var displayName sql.NullString
	require.NoError(t, f.db.NewSelect().Table("contacts").Column("display_name").Where("id = ?", contactID).Scan(ctx, &displayName))
	require.False(t, displayName.Valid, "AI 写入了显示名称 = %q", displayName.String)
	methods := make([]servermodels.ContactMethod, 0)
	require.NoError(t, f.db.NewSelect().Model(&methods).Where("cm.contact_id = ?", contactID).OrderExpr("cm.type").Scan(ctx))
	require.Len(t, methods, 2, "AI 添加的联系方式")
	require.Equal(t, "wang@example.com", methods[0].Value)
	require.True(t, methods[0].IsPrimary)
	require.Equal(t, "+8613800000000", methods[1].Value)
	require.True(t, methods[1].IsPrimary)

	// 客服改写 AI 的值后 AI 不再改动，AI 可以更新自己写的值，客服添加的标签不受影响。
	require.NoError(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, city.ID, "北京"))
	require.NoError(t, contactprofileaction.NewAddTagAction(f.db).Execute(ctx, f.member, contactID, manualTag.ID))
	caller.text = `{"fields":[{"name":"城市","value":"广州"},{"name":"套餐","value":"基础版"}],"emails":[],"phones":[]}`
	require.NoError(t, worker.ExtractContactProfile(ctx, input))
	require.NotContains(t, caller.input, "北京", "模型资料包含客服改写的字段")
	profile, err = contactprofileaction.Load(ctx, f.db, f.owner.Workspace.ID, contactID)
	require.NoError(t, err)
	for _, value := range profile.Fields {
		values[value.FieldID] = value
	}
	require.Equal(t, "北京", values[city.ID].Value, "客服改写后的城市")
	require.Equal(t, domain.ContactProfileSourceMember, values[city.ID].Source)
	require.Nil(t, values[city.ID].SourceSession)
	require.Equal(t, plan.Options[0].ID, values[plan.ID].Value, "AI 更新的套餐")
	require.Equal(t, domain.ContactProfileSourceAI, values[plan.ID].Source)

	// 客服修改过小结的周期重开再关闭时仍投递抽取任务，上一次关闭的任务不再写入。
	_, err = servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db).Execute(ctx, f.member, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: session.ID, Summary: "客服修改的小结",
	})
	require.NoError(t, err)
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	reclosed := loadExtractContactProfileInput(t, session.ID, tasks, testEnqueuer)
	require.False(t, reclosed.ClosedAt.Equal(input.ClosedAt), "客服修改过小结的周期再次关闭时没有投递抽取任务")
	callsBefore := caller.calls
	require.NoError(t, worker.ExtractContactProfile(ctx, input))
	require.Equal(t, callsBefore, caller.calls, "周期再次关闭后旧任务仍调用了模型")

	// 客户新开周期后关闭，新周期的结果先写入；更早关闭的周期的任务随后执行时不覆盖，较新周期重开后同样不覆盖。
	age, err := createField.Execute(ctx, f.owner, contactprofileaction.FieldInput{Name: "年龄", Type: domain.ContactFieldTypeNumber, AIInstruction: "客户的年龄"})
	require.NoError(t, err)
	complaint, err := createTag.Execute(ctx, f.owner, contactprofileaction.TagInput{Name: "投诉", AIInstruction: "客户表达了投诉"})
	require.NoError(t, err)
	require.NoError(t, contactprofileaction.NewSetFieldValueAction(f.db).Execute(ctx, f.member, contactID, city.ID, ""))
	_, err = f.visitorMessage(ctx, "我想升级到专业版")
	require.NoError(t, err)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, tasks).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	next := loadSummarySession(t, f.db, f.conversationID)
	require.NotEqual(t, session.ID, next.ID, "客户新消息没有开启新周期")
	// 模型把数字字段输出为 JSON 数字；投诉标签概率不足不添加。
	decider.answers = map[string]decision.Answer{"tag_0": {Kind: decision.KindYesNo, Probability: 0.5}}
	caller.text = `{"name":"","fields":[{"name":"城市","value":"广州"},{"name":"套餐","value":"专业版"},{"name":"年龄","value":30}],"emails":[],"phones":[]}`
	require.NoError(t, worker.ExtractContactProfile(ctx, loadExtractContactProfileInput(t, next.ID, tasks, testEnqueuer)))
	// 较新的周期重开后，更早关闭的周期的任务仍不覆盖其结果。
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	caller.text = `{"name":"","fields":[{"name":"套餐","value":"基础版"}],"emails":[],"phones":[]}`
	require.NoError(t, worker.ExtractContactProfile(ctx, reclosed))
	profile, err = contactprofileaction.Load(ctx, f.db, f.owner.Workspace.ID, contactID)
	require.NoError(t, err)
	values = arr.KeyBy(profile.Fields, func(value contactprofileaction.FieldValue) string { return value.FieldID })
	require.Equal(t, "广州", values[city.ID].Value, "客服清空后 AI 重新填写的城市")
	require.Equal(t, domain.ContactProfileSourceAI, values[city.ID].Source)
	require.Equal(t, plan.Options[1].ID, values[plan.ID].Value, "更早关闭的周期覆盖了套餐")
	require.NotNil(t, values[plan.ID].SourceSession)
	require.Equal(t, next.OpeningMessageID, values[plan.ID].SourceSession.OpeningMessageID)
	require.Equal(t, "30", values[age.ID].Value, "数字取值")
	for _, tag := range profile.Tags {
		require.NotEqual(t, complaint.ID, tag.ID, "概率不足的标签被添加")
	}
}

// loadExtractContactProfileInput 返回登记器中指定周期最近一次投递的联系人资料抽取任务输入。
func loadExtractContactProfileInput(t *testing.T, serviceSessionID string, recorders ...*servertest.Tasks) servicesummary.ExtractContactProfileInput {
	t.Helper()
	return servertest.LatestInput(t, servicesummary.ExtractContactProfileActionName, func(input servicesummary.ExtractContactProfileInput) bool {
		return input.ServiceSessionID == serviceSessionID
	}, recorders...)
}
