//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// contactOps 持有联系人及其档案的 Action 和 Query。
type contactOps struct {
	listContactFields    *contactprofileaction.ListFieldsQuery
	createContactField   *contactprofileaction.CreateFieldAction
	updateContactField   *contactprofileaction.UpdateFieldAction
	deleteContactField   *contactprofileaction.DeleteFieldAction
	listContactTags      *contactprofileaction.ListTagsQuery
	createContactTag     *contactprofileaction.CreateTagAction
	updateContactTag     *contactprofileaction.UpdateTagAction
	deleteContactTag     *contactprofileaction.DeleteTagAction
	setContactFieldValue *contactprofileaction.SetFieldValueAction
	addContactTag        *contactprofileaction.AddTagAction
	removeContactTag     *contactprofileaction.RemoveTagAction
	listContacts         *contactaction.ListContactsQuery
	getContact           *contactaction.GetContactQuery
	createContact        *contactaction.CreateContactAction
	updateContact        *contactaction.UpdateContactAction
	deleteContact        *contactaction.DeleteContactAction
	restoreContact       *contactaction.RestoreContactAction
	// files 解析文件地址。
	files *fileOps
}

// newContactOps 创建联系人及其档案的业务实现依赖。
func newContactOps(db *bun.DB, files *fileOps) *contactOps {
	return &contactOps{
		listContactFields:    contactprofileaction.NewListFieldsQuery(db),
		createContactField:   contactprofileaction.NewCreateFieldAction(db),
		updateContactField:   contactprofileaction.NewUpdateFieldAction(db),
		deleteContactField:   contactprofileaction.NewDeleteFieldAction(db),
		listContactTags:      contactprofileaction.NewListTagsQuery(db),
		createContactTag:     contactprofileaction.NewCreateTagAction(db),
		updateContactTag:     contactprofileaction.NewUpdateTagAction(db),
		deleteContactTag:     contactprofileaction.NewDeleteTagAction(db),
		setContactFieldValue: contactprofileaction.NewSetFieldValueAction(db),
		addContactTag:        contactprofileaction.NewAddTagAction(db),
		removeContactTag:     contactprofileaction.NewRemoveTagAction(db),
		listContacts:         contactaction.NewListContactsQuery(db),
		getContact:           contactaction.NewGetContactQuery(db),
		createContact:        contactaction.NewCreateContactAction(db),
		updateContact:        contactaction.NewUpdateContactAction(db),
		deleteContact:        contactaction.NewDeleteContactAction(db),
		restoreContact:       contactaction.NewRestoreContactAction(db),
		files:                files,
	}
}

// ListContacts 返回联系人列表。
func (o *contactOps) ListContacts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactListInput) (appservice.ContactList, error) {
	output, err := o.listContacts.Execute(ctx, identity, contactaction.ListInput{
		Query: input.Query, Stage: domain.ContactStage(support.Deref(input.Stage)), ChannelID: input.ChannelID, MethodType: domain.ContactMethodType(support.Deref(input.MethodType)),
		TagID: input.TagID, Sort: input.Sort, Page: input.Page, PageSize: input.PageSize, Deleted: input.Deleted,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.ContactList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, contactFieldKeys))
	}
	if err != nil {
		return appservice.ContactList{}, contactError(meta, err, i18n.ErrorContactListFailed)
	}
	avatarFileIDs := arr.Map(output.Contacts, func(contact contactaction.ContactSummary) *string { return contact.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ContactList{}, contactError(meta, err, i18n.ErrorContactListFailed)
	}
	contacts := make([]appservice.ContactSummary, 0, len(output.Contacts))
	for _, contact := range output.Contacts {
		tags := arr.Map(contact.Tags, func(tag contactaction.TagSummary) appservice.ContactTagSummary {
			return appservice.ContactTagSummary{ID: tag.ID, Name: tag.Name}
		})
		contacts = append(contacts, appservice.ContactSummary{
			ID: contact.ID, Number: contact.Number, DisplayName: contact.DisplayName, AvatarURL: optionalFileURL(avatarURLs, contact.AvatarFileID), Stage: contact.Stage, PrimaryEmail: contact.PrimaryEmail,
			PrimaryPhone: contact.PrimaryPhone, SourceChannelName: contact.SourceChannelName, CreatedAt: contact.CreatedAt, DeletedAt: contact.DeletedAt, Tags: tags,
		})
	}
	return appservice.ContactList{Contacts: contacts, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetContact 返回联系人详情。
func (o *contactOps) GetContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) (appservice.Contact, error) {
	contact, err := o.getContact.Execute(ctx, identity, contactID)
	if err != nil {
		return appservice.Contact{}, contactError(meta, err, i18n.ErrorContactReadFailed)
	}
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactReadFailed)
}

// CreateContact 创建联系人。
func (o *contactOps) CreateContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactInput) (appservice.Contact, error) {
	contact, err := o.createContact.Execute(ctx, identity, contactInput(input))
	if err != nil {
		return appservice.Contact{}, contactMutationError(meta, err, i18n.ErrorContactCreateFailed)
	}
	slog.InfoContext(ctx, "联系人创建成功", "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactCreateFailed)
}

// UpdateContact 修改联系人。
func (o *contactOps) UpdateContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string, input appservice.ContactInput) (appservice.Contact, error) {
	contact, err := o.updateContact.Execute(ctx, identity, contactID, contactInput(input))
	if err != nil {
		return appservice.Contact{}, contactMutationError(meta, err, i18n.ErrorContactUpdateFailed)
	}
	slog.InfoContext(ctx, "联系人更新成功", "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactUpdateFailed)
}

// DeleteContact 将联系人移入回收站。
func (o *contactOps) DeleteContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) error {
	if err := o.deleteContact.Execute(ctx, identity, contactID); err != nil {
		return contactError(meta, err, i18n.ErrorContactDeleteFailed)
	}
	slog.InfoContext(ctx, "联系人移入回收站", "contact_id", contactID)
	return nil
}

// RestoreContact 恢复联系人。
func (o *contactOps) RestoreContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) (appservice.Contact, error) {
	contact, err := o.restoreContact.Execute(ctx, identity, contactID)
	if err != nil {
		return appservice.Contact{}, contactError(meta, err, i18n.ErrorContactRestoreFailed)
	}
	slog.InfoContext(ctx, "联系人恢复成功", "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactRestoreFailed)
}

// contactMutationError 转换联系人写入校验和操作错误。
func contactMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return contactMutationErrors.Translate(meta, err, failureKey)
}

// contactErrors 是联系人读取和删除的错误转换规则。
var contactErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(contactaction.ErrNotFound, dispatch.NotFound(i18n.ErrorContactNotFound)),
})

// contactMutationErrors 是联系人写入的错误转换规则。
var contactMutationErrors = dispatch.Catalogs(dispatch.Catalog{dispatch.FieldRule(contactFieldKeys)}, contactErrors)

// contactError 转换联系人读取和删除错误。
func contactError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return contactErrors.Translate(meta, err, failureKey)
}

// contactInput 把联系人契约输入转换为动作层输入。
func contactInput(input appservice.ContactInput) contactaction.ContactInput {
	methods := arr.Map(input.Methods, func(method appservice.ContactMethodInput) contactaction.MethodInput {
		return contactaction.MethodInput{Type: method.Type, Value: method.Value, Label: method.Label, IsPrimary: method.IsPrimary}
	})
	return contactaction.ContactInput{DisplayName: input.DisplayName, ChannelID: input.ChannelID, Stage: input.Stage, Notes: input.Notes, Methods: methods}
}

// contactWithAvatar 解析联系人头像地址并转换详情契约。
func (o *contactOps) contactWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contact *contactaction.ContactDetail, failureKey i18n.Key) (appservice.Contact, error) {
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, contact.AvatarFileID)
	if err != nil {
		return appservice.Contact{}, contactError(meta, err, failureKey)
	}
	output := contactFromAction(contact)
	output.AvatarURL = optionalFileURL(avatarURLs, contact.AvatarFileID)
	return output, nil
}

// contactFromAction 把联系人详情转换为应用契约。
func contactFromAction(contact *contactaction.ContactDetail) appservice.Contact {
	methods := arr.Map(contact.Methods, func(method contactaction.ContactMethod) appservice.ContactMethod {
		return appservice.ContactMethod{Type: method.Type, Value: method.Value, Label: method.Label, IsPrimary: method.IsPrimary}
	})
	identities := arr.Map(contact.ChannelIdentities, func(identity contactaction.ChannelIdentity) appservice.ContactChannelIdentity {
		return appservice.ContactChannelIdentity{
			ChannelID: identity.ChannelID, ChannelName: identity.ChannelName, ExternalID: identity.ExternalID, DisplayName: identity.DisplayName,
		}
	})
	return appservice.Contact{
		Name: contact.Name,
		Contact: appservice.ContactRecord{
			ID: contact.Contact.ID, Number: contact.Contact.Number, SourceChannelID: contact.Contact.SourceChannelID, DisplayName: contact.Contact.DisplayName,
			Stage: contact.Contact.Stage, Notes: contact.Contact.Notes, CreatedAt: contact.Contact.CreatedAt,
		},
		SourceChannel: appservice.ContactSourceChannel{ID: contact.SourceChannel.ID, Type: contact.SourceChannel.Type, Name: contact.SourceChannel.Name},
		Methods:       methods, ChannelIdentities: identities, Profile: contactProfileFromAction(contact.Profile),
	}
}

// contactFieldKeys 把联系人校验错误码映射为本地化文案键。
var contactFieldKeys = map[common.FieldCode]i18n.Key{
	contactaction.ValidationIdentityRequired: i18n.FieldContactIdentityRequired,
	contactaction.ValidationChannelInvalid:   i18n.FieldContactChannelInvalid,
	contactaction.ValidationChannelImmutable: i18n.FieldContactChannelImmutable,
	contactaction.ValidationMethodInvalid:    i18n.FieldContactMethodInvalid,
	contactaction.ValidationMethodDuplicate:  i18n.FieldContactMethodDuplicate,
	contactaction.ValidationPrimaryDuplicate: i18n.FieldContactPrimaryDuplicate,
}
