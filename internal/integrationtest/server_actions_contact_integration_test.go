//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// testContacts 覆盖联系人的创建、联系方式保留、渠道不可变校验、软删除恢复与失效身份拦截。
func (s *serverActionsFixture) testContacts(t *testing.T) {
	db, resolveIdentity, loggedIn, login, profileEmail := s.db, s.resolveIdentity, s.loggedIn, s.login, s.profileEmail
	createContact := contactaction.NewCreateContactAction(db)
	_, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{
		DisplayName: "无效渠道联系人",
		ChannelID:   "00000000-0000-0000-0000-000000000099",
		Stage:       domain.ContactStageVisitor,
	})
	var channelValidation *contactaction.ValidationError
	require.ErrorAs(t, err, &channelValidation, "invalid channel")
	require.Equal(t, contactaction.ValidationChannelInvalid, channelValidation.Fields["channelId"], "invalid channel")

	contact, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{
		DisplayName: "林晓",
		ChannelID:   s.channel.ID,
		Stage:       domain.ContactStageLead,
		Notes:       "采购负责人",
		Methods: []contactaction.MethodInput{
			{Type: domain.ContactMethodTypeEmail, Value: "LIN@example.com", Label: "工作"},
			{Type: domain.ContactMethodTypeEmail, Value: "lin.private@example.com", Label: "私人"},
			{Type: domain.ContactMethodTypePhone, Value: "+86 138-0000-0000", Label: "手机"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, new("林晓"), contact.Contact.DisplayName)
	require.Equal(t, s.channel.ID, contact.Contact.SourceChannelID)
	require.Equal(t, s.channel.ID, contact.SourceChannel.ID)
	require.Len(t, contact.Methods, 3)
	type methodIdentity struct {
		typeName string
		value    string
	}
	storedMethods := make([]servermodels.ContactMethod, 0)
	require.NoError(t, db.NewSelect().
		Model(&storedMethods).
		Where("cm.workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("cm.contact_id = ?", contact.Contact.ID).
		Scan(context.Background()))
	preservedMethods := arr.KeyBy(storedMethods, func(method servermodels.ContactMethod) methodIdentity {
		return methodIdentity{typeName: method.Type, value: method.Value}
	})
	preservedInputs := make([]contactaction.MethodInput, 0, len(contact.Methods))
	for _, method := range contact.Methods {
		preservedInputs = append(preservedInputs, contactaction.MethodInput{
			Type: method.Type, Value: method.Value, Label: support.Deref(method.Label), IsPrimary: method.IsPrimary,
		})
	}
	preservedContact, err := contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
		DisplayName: "林晓（已确认）",
		ChannelID:   s.channel.ID,
		Stage:       domain.ContactStageLead,
		Notes:       "采购负责人",
		Methods:     preservedInputs,
	})
	require.NoError(t, err)
	require.Len(t, preservedContact.Methods, len(contact.Methods), "preserved methods count")
	afterMethods := make([]servermodels.ContactMethod, 0)
	require.NoError(t, db.NewSelect().
		Model(&afterMethods).
		Where("cm.workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("cm.contact_id = ?", contact.Contact.ID).
		Scan(context.Background()))
	for _, method := range afterMethods {
		before, ok := preservedMethods[methodIdentity{typeName: method.Type, value: method.Value}]
		require.True(t, ok, "contact method was recreated: after=%#v", method)
		require.Equal(t, before.ID, method.ID)
		require.Equal(t, before.Label, method.Label)
		require.Equal(t, before.IsPrimary, method.IsPrimary)
		require.True(t, method.CreatedAt.Equal(before.CreatedAt), "contact method created at: before=%v after=%v", before.CreatedAt, method.CreatedAt)
		require.True(t, method.UpdatedAt.Equal(before.UpdatedAt), "contact method updated at: before=%v after=%v", before.UpdatedAt, method.UpdatedAt)
	}

	contactList := contactaction.NewListContactsQuery(db)
	activeContacts, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{
		Query: "lin@example", Stage: domain.ContactStageLead, ChannelID: s.channel.ID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 1, activeContacts.Page.Total)
	require.Len(t, activeContacts.Contacts, 1)
	require.NotNil(t, activeContacts.Contacts[0].PrimaryEmail)
	require.Equal(t, s.channel.Name, activeContacts.Contacts[0].SourceChannelName)

	// 联系人编号在工作区内按创建顺序递增，列表可按访客编号名称检索。
	next, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{DisplayName: "周宁", ChannelID: s.channel.ID, Stage: domain.ContactStageVisitor})
	require.NoError(t, err)
	require.GreaterOrEqual(t, contact.Contact.Number, int64(1))
	require.Equal(t, contact.Contact.Number+1, next.Contact.Number)
	numbered, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{
		Query: fmt.Sprintf("访客 #%d", next.Contact.Number), Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Len(t, numbered.Contacts, 1)
	require.Equal(t, next.Contact.ID, numbered.Contacts[0].ID)
	require.Equal(t, next.Contact.Number, numbered.Contacts[0].Number)

	_, err = contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
		DisplayName: "林晓",
		ChannelID:   "00000000-0000-0000-0000-000000000099",
		Stage:       domain.ContactStageLead,
		Methods:     []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "lin@example.com"}},
	})
	var immutableChannelValidation *contactaction.ValidationError
	require.ErrorAs(t, err, &immutableChannelValidation, "immutable channel")
	require.Equal(t, contactaction.ValidationChannelImmutable, immutableChannelValidation.Fields["channelId"], "immutable channel")

	updatedContact, err := contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
		DisplayName: "林晓（采购）",
		ChannelID:   s.channel.ID,
		Stage:       domain.ContactStageCustomer,
		Methods:     []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "lin@example.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, domain.ContactStageCustomer, updatedContact.Contact.Stage)
	require.Len(t, updatedContact.Methods, 1)

	_, err = db.NewUpdate().Table("users").Set("status = 'inactive'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
	// 成员停用只影响该工作区：账号仍能登录，但不能解析出该工作区的成员身份。
	inactiveSession, err := login.Execute(context.Background(), authaction.LoginInput{Email: profileEmail, Password: "password123"})
	require.NoError(t, err, "inactive member account login")
	_, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Workspace.ID, inactiveSession.Token)
	require.ErrorIs(t, err, authaction.ErrMembershipNotFound, "inactive member identity")
	require.ErrorIs(t, contactaction.NewDeleteContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID), identityaction.ErrInvalid, "inactive user delete")
	_, err = db.NewUpdate().Table("users").Set("status = 'active'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, contactaction.NewDeleteContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID))
	deletedContacts, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{Deleted: true, Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, deletedContacts.Page.Total)
	require.Len(t, deletedContacts.Contacts, 1)
	_, err = db.NewUpdate().Table("users").Set("status = 'inactive'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
	_, err = contactaction.NewRestoreContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID)
	require.ErrorIs(t, err, identityaction.ErrInvalid, "inactive user restore")
	_, err = db.NewUpdate().Table("users").Set("status = 'active'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
	_, err = contactaction.NewRestoreContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID)
	require.NoError(t, err)
}

// testContactProfile 覆盖联系人字段与标签的定义维护、取值校验、单选选项移除清空取值、标签筛选和删除定义时的级联清理。
func (s *serverActionsFixture) testContactProfile(t *testing.T) {
	db, loggedIn := s.db, s.loggedIn
	ctx := context.Background()
	identity := loggedIn.Identity
	contact, err := contactaction.NewCreateContactAction(db).Execute(ctx, identity, contactaction.ContactInput{
		DisplayName: "档案联系人", ChannelID: s.channel.ID, Stage: domain.ContactStageCustomer, Notes: "偏好邮件沟通",
	})
	require.NoError(t, err)
	createField := contactprofileaction.NewCreateFieldAction(db)
	company, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: " 公司 ", Type: domain.ContactFieldTypeText})
	require.NoError(t, err)
	require.Equal(t, "公司", company.Name)
	require.Empty(t, company.Options)
	var duplicate *common.FieldError
	_, err = createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeNumber})
	require.ErrorAs(t, err, &duplicate, "duplicate field")
	require.Equal(t, contactprofileaction.ValidationNameDuplicate, duplicate.Fields["name"], "duplicate field")
	seats, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
	require.NoError(t, err)
	plan, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}}})
	require.NoError(t, err)
	require.Len(t, plan.Options, 2)
	require.NotEmpty(t, plan.Options[0].ID)
	require.NotEmpty(t, plan.Options[1].ID)

	setValue := contactprofileaction.NewSetFieldValueAction(db)
	require.NoError(t, setValue.Execute(ctx, identity, contact.Contact.ID, company.ID, "  Acme  "))
	var invalid *common.FieldError
	for _, input := range []string{"二十", "1e2", "1.", ".5"} {
		err := setValue.Execute(ctx, identity, contact.Contact.ID, seats.ID, input)
		require.ErrorAs(t, err, &invalid, "invalid number %q", input)
		require.Equal(t, contactprofileaction.ValidationValueInvalid, invalid.Fields["value"], "invalid number %q", input)
	}
	// 数字按十进制文本规范化，不经过二进制浮点。
	for _, number := range []struct{ input, want string }{{"9007199254740993", "9007199254740993"}, {"-0.00", "0"}, {"+020.50", "20.5"}} {
		input, want := number.input, number.want
		require.NoError(t, setValue.Execute(ctx, identity, contact.Contact.ID, seats.ID, input))
		stored := &servermodels.ContactFieldValue{}
		require.NoError(t, db.NewSelect().Model(stored).Where("cfv.contact_id = ? AND cfv.field_id = ?", contact.Contact.ID, seats.ID).Scan(ctx))
		require.Equal(t, want, stored.Value, "normalized number %q", input)
	}
	// 档案实际变化时更新联系人的更新时间。
	var beforeTouch time.Time
	require.NoError(t, db.NewSelect().Table("contacts").Column("updated_at").Where("id = ?", contact.Contact.ID).Scan(ctx, &beforeTouch))
	err = setValue.Execute(ctx, identity, contact.Contact.ID, plan.ID, "unknown-option")
	require.ErrorAs(t, err, &invalid, "invalid option")
	require.Equal(t, contactprofileaction.ValidationValueInvalid, invalid.Fields["value"], "invalid option")
	require.NoError(t, setValue.Execute(ctx, identity, contact.Contact.ID, plan.ID, plan.Options[1].ID))
	var afterTouch time.Time
	require.NoError(t, db.NewSelect().Table("contacts").Column("updated_at").Where("id = ?", contact.Contact.ID).Scan(ctx, &afterTouch))
	require.True(t, afterTouch.After(beforeTouch), "contact updated_at = %v, want after %v", afterTouch, beforeTouch)

	createTag := contactprofileaction.NewCreateTagAction(db)
	vip, err := createTag.Execute(ctx, identity, contactprofileaction.TagInput{Name: "VIP"})
	require.NoError(t, err)
	_, err = createTag.Execute(ctx, identity, contactprofileaction.TagInput{Name: "vip"})
	require.ErrorAs(t, err, &duplicate, "duplicate tag")
	require.Equal(t, contactprofileaction.ValidationNameDuplicate, duplicate.Fields["name"], "duplicate tag")
	addTag := contactprofileaction.NewAddTagAction(db)
	for range 2 {
		require.NoError(t, addTag.Execute(ctx, identity, contact.Contact.ID, vip.ID))
	}

	detail, err := contactaction.NewGetContactQuery(db).Execute(ctx, identity, contact.Contact.ID)
	require.NoError(t, err)
	values := map[string]string{}
	for _, value := range detail.Profile.Fields {
		require.Equal(t, domain.ContactProfileSourceMember, value.Source, "field value source")
		values[value.FieldID] = value.Value
	}
	require.Equal(t, "Acme", values[company.ID])
	require.Equal(t, "20.5", values[seats.ID])
	require.Equal(t, plan.Options[1].ID, values[plan.ID])
	require.Len(t, detail.Profile.Tags, 1)
	require.Equal(t, vip.ID, detail.Profile.Tags[0].ID)

	agentProfile, err := contactprofileaction.LoadAgentProfile(ctx, db, identity.Workspace.ID, contact.Contact.ID)
	require.NoError(t, err)
	require.Equal(t, string(domain.ContactStageCustomer), agentProfile.Stage)
	require.Equal(t, "偏好邮件沟通", agentProfile.Notes)
	require.Equal(t, "专业版", agentProfile.Fields["套餐"])
	require.Equal(t, "20.5", agentProfile.Fields["席位数"])
	require.Len(t, agentProfile.Tags, 1)
	require.Equal(t, "VIP", agentProfile.Tags[0])

	renamedTag, err := contactprofileaction.NewUpdateTagAction(db).Execute(ctx, identity, vip.ID, contactprofileaction.TagInput{Name: "重要客户"})
	require.NoError(t, err)
	require.Equal(t, "重要客户", renamedTag.Name)
	_, err = contactprofileaction.NewUpdateTagAction(db).Execute(ctx, identity, vip.ID, contactprofileaction.TagInput{Name: "VIP"})
	require.NoError(t, err)

	listed, err := contactaction.NewListContactsQuery(db).Execute(ctx, identity, contactaction.ListInput{TagID: vip.ID})
	require.NoError(t, err)
	require.Len(t, listed.Contacts, 1)
	require.Equal(t, contact.Contact.ID, listed.Contacts[0].ID)
	require.Len(t, listed.Contacts[0].Tags, 1)
	require.Equal(t, "VIP", listed.Contacts[0].Tags[0].Name)

	// 移除被选中的选项后该字段取值随之清空，类型不可修改。
	updateField := contactprofileaction.NewUpdateFieldAction(db)
	var immutable *common.FieldError
	_, err = updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeText})
	require.ErrorAs(t, err, &immutable, "change field type")
	require.Equal(t, contactprofileaction.ValidationFieldTypeImmutable, immutable.Fields["type"], "change field type")
	_, err = updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{plan.Options[0], {ID: plan.Options[0].ID, Name: "重复编号"}}})
	require.ErrorAs(t, err, &invalid, "duplicate option id")
	require.Equal(t, contactprofileaction.ValidationOptionInvalid, invalid.Fields["options"], "duplicate option id")
	renamed, err := updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "订阅套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{plan.Options[0], {Name: "企业版"}}})
	require.NoError(t, err)
	require.Equal(t, "订阅套餐", renamed.Name)
	require.Len(t, renamed.Options, 2)
	require.Equal(t, plan.Options[0].ID, renamed.Options[0].ID)
	require.NotEqual(t, plan.Options[1].ID, renamed.Options[1].ID)
	require.NoError(t, setValue.Execute(ctx, identity, contact.Contact.ID, company.ID, ""))
	profile, err := contactprofileaction.Load(ctx, db, identity.Workspace.ID, contact.Contact.ID)
	require.NoError(t, err)
	require.Len(t, profile.Fields, 1)
	require.Equal(t, seats.ID, profile.Fields[0].FieldID)

	require.NoError(t, contactprofileaction.NewDeleteFieldAction(db).Execute(ctx, identity, seats.ID))
	require.NoError(t, contactprofileaction.NewDeleteTagAction(db).Execute(ctx, identity, vip.ID))
	profile, err = contactprofileaction.Load(ctx, db, identity.Workspace.ID, contact.Contact.ID)
	require.NoError(t, err)
	require.Empty(t, profile.Fields)
	require.Empty(t, profile.Tags)
	orphans, err := db.NewSelect().Model((*servermodels.ContactFieldValue)(nil)).Where("field_id = ?", seats.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), orphans, "orphan field values")
	for _, field := range []string{company.ID, plan.ID} {
		require.NoError(t, contactprofileaction.NewDeleteFieldAction(db).Execute(ctx, identity, field))
	}
	require.NoError(t, contactaction.NewDeleteContactAction(db).Execute(ctx, identity, contact.Contact.ID))
}
