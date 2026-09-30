//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
}

// newContactOps 创建联系人及其档案的业务实现依赖。
func newContactOps(db *bun.DB) contactOps {
	return contactOps{
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
	}
}

// ListContacts 返回联系人列表。
func (o *directOperations) ListContacts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactListInput) (appservice.ContactList, error) {
	output, err := o.listContacts.Execute(ctx, identity, contactaction.ListInput{
		Query: input.Query, Stage: optionalDomain[appservice.ContactStage, domain.ContactStage](input.Stage), ChannelID: input.ChannelID, MethodType: optionalDomain[appservice.ContactMethodType, domain.ContactMethodType](input.MethodType),
		TagID: input.TagID, Sort: domain.ContactSort(input.Sort), Page: input.Page, PageSize: input.PageSize, Deleted: input.Deleted,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.ContactList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, contactFieldKeys(validationError.Fields))
	}
	if err != nil {
		return appservice.ContactList{}, o.contactError(ctx, meta, err, i18n.ErrorContactListFailed)
	}
	avatarFileIDs := make([]*string, 0, len(output.Contacts))
	for _, contact := range output.Contacts {
		avatarFileIDs = append(avatarFileIDs, contact.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.ContactList{}, o.contactError(ctx, meta, err, i18n.ErrorContactListFailed)
	}
	contacts := make([]appservice.ContactSummary, 0, len(output.Contacts))
	for _, contact := range output.Contacts {
		tags := make([]appservice.ContactTagSummary, 0, len(contact.Tags))
		for _, tag := range contact.Tags {
			tags = append(tags, appservice.ContactTagSummary{ID: tag.ID, Name: tag.Name})
		}
		contacts = append(contacts, appservice.ContactSummary{
			ID: contact.ID, Number: contact.Number, DisplayName: contact.DisplayName, AvatarURL: optionalFileURL(avatarURLs, contact.AvatarFileID), Stage: appservice.ContactStage(contact.Stage), PrimaryEmail: contact.PrimaryEmail,
			PrimaryPhone: contact.PrimaryPhone, SourceChannelName: contact.SourceChannelName, CreatedAt: contact.CreatedAt, DeletedAt: contact.DeletedAt, Tags: tags,
		})
	}
	return appservice.ContactList{Contacts: contacts, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetContact 返回联系人详情。
func (o *directOperations) GetContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) (appservice.Contact, error) {
	contact, err := o.getContact.Execute(ctx, identity, contactID)
	if err != nil {
		return appservice.Contact{}, o.contactError(ctx, meta, err, i18n.ErrorContactReadFailed)
	}
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactReadFailed)
}

// CreateContact 创建联系人。
func (o *directOperations) CreateContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactInput) (appservice.Contact, error) {
	contact, err := o.createContact.Execute(ctx, identity, contactInput(input))
	if err != nil {
		return appservice.Contact{}, o.contactMutationError(ctx, meta, err, i18n.ErrorContactCreateFailed)
	}
	slog.Info("联系人创建成功", "organization_id", identity.Organization.ID, "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactCreateFailed)
}

// UpdateContact 修改联系人。
func (o *directOperations) UpdateContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string, input appservice.ContactInput) (appservice.Contact, error) {
	contact, err := o.updateContact.Execute(ctx, identity, contactID, contactInput(input))
	if err != nil {
		return appservice.Contact{}, o.contactMutationError(ctx, meta, err, i18n.ErrorContactUpdateFailed)
	}
	slog.Info("联系人更新成功", "organization_id", identity.Organization.ID, "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactUpdateFailed)
}

// DeleteContact 将联系人移入回收站。
func (o *directOperations) DeleteContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) error {
	if err := o.deleteContact.Execute(ctx, identity, contactID); err != nil {
		return o.contactError(ctx, meta, err, i18n.ErrorContactDeleteFailed)
	}
	slog.Info("联系人移入回收站", "organization_id", identity.Organization.ID, "contact_id", contactID)
	return nil
}

// RestoreContact 恢复联系人。
func (o *directOperations) RestoreContact(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID string) (appservice.Contact, error) {
	contact, err := o.restoreContact.Execute(ctx, identity, contactID)
	if err != nil {
		return appservice.Contact{}, o.contactError(ctx, meta, err, i18n.ErrorContactRestoreFailed)
	}
	slog.Info("联系人恢复成功", "organization_id", identity.Organization.ID, "contact_id", contact.Contact.ID)
	return o.contactWithAvatar(ctx, meta, identity, contact, i18n.ErrorContactRestoreFailed)
}

// contactMutationError 转换联系人写入校验和操作错误。
func (o *directOperations) contactMutationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, contactFieldKeys(validationError.Fields))
	}
	return o.contactError(ctx, meta, err, failureKey)
}

// contactError 转换联系人读取和删除错误。
func (o *directOperations) contactError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, contactaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorContactNotFound)
	}
	slog.Warn("联系人操作失败", "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
}

// contactInput 把联系人契约输入转换为动作层输入。
func contactInput(input appservice.ContactInput) contactaction.ContactInput {
	methods := make([]contactaction.MethodInput, 0, len(input.Methods))
	for _, method := range input.Methods {
		methods = append(methods, contactaction.MethodInput{Type: domain.ContactMethodType(method.Type), Value: method.Value, Label: method.Label, IsPrimary: method.IsPrimary})
	}
	return contactaction.ContactInput{DisplayName: input.DisplayName, ChannelID: input.ChannelID, Stage: domain.ContactStage(input.Stage), Notes: input.Notes, Methods: methods}
}

// contactWithAvatar 解析联系人头像地址并转换详情契约。
func (o *directOperations) contactWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contact *contactaction.ContactDetail, failureKey i18n.Key) (appservice.Contact, error) {
	avatarURLs, err := o.optionalFileURLs(ctx, identity, contact.AvatarFileID)
	if err != nil {
		return appservice.Contact{}, o.contactError(ctx, meta, err, failureKey)
	}
	output := contactFromAction(contact)
	output.AvatarURL = optionalFileURL(avatarURLs, contact.AvatarFileID)
	return output, nil
}

// contactFromAction 把联系人详情转换为应用契约。
func contactFromAction(contact *contactaction.ContactDetail) appservice.Contact {
	methods := make([]appservice.ContactMethod, 0, len(contact.Methods))
	for _, method := range contact.Methods {
		methods = append(methods, appservice.ContactMethod{Type: appservice.ContactMethodType(method.Type), Value: method.Value, Label: method.Label, IsPrimary: method.IsPrimary})
	}
	identities := make([]appservice.ContactChannelIdentity, 0, len(contact.ChannelIdentities))
	for _, identity := range contact.ChannelIdentities {
		identities = append(identities, appservice.ContactChannelIdentity{
			ChannelID: identity.ChannelID, ChannelName: identity.ChannelName, ExternalID: identity.ExternalID, DisplayName: identity.DisplayName,
		})
	}
	return appservice.Contact{
		Name: contact.Name,
		Contact: appservice.ContactRecord{
			ID: contact.Contact.ID, Number: contact.Contact.Number, SourceChannelID: contact.Contact.SourceChannelID, DisplayName: contact.Contact.DisplayName,
			Stage: appservice.ContactStage(contact.Contact.Stage), Notes: contact.Contact.Notes, CreatedAt: contact.Contact.CreatedAt,
		},
		SourceChannel: appservice.ContactSourceChannel{ID: contact.SourceChannel.ID, Type: appservice.ChannelType(contact.SourceChannel.Type), Name: contact.SourceChannel.Name},
		Methods:       methods, ChannelIdentities: identities, Profile: contactProfileFromAction(contact.Profile),
	}
}

// contactFieldKeys 把联系人校验错误码映射为本地化文案键。
func contactFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		contactaction.ValidationIdentityRequired: i18n.FieldContactIdentityRequired, contactaction.ValidationChannelRequired: i18n.FieldContactChannelRequired,
		contactaction.ValidationChannelInvalid: i18n.FieldContactChannelInvalid, contactaction.ValidationChannelImmutable: i18n.FieldContactChannelImmutable,
		contactaction.ValidationNameTooLong: i18n.FieldContactNameTooLong, contactaction.ValidationStageInvalid: i18n.FieldContactStageInvalid,
		contactaction.ValidationNotesTooLong: i18n.FieldContactNotesTooLong, contactaction.ValidationMethodsTooMany: i18n.FieldContactMethodsTooMany,
		contactaction.ValidationMethodInvalid: i18n.FieldContactMethodInvalid, contactaction.ValidationMethodDuplicate: i18n.FieldContactMethodDuplicate,
		contactaction.ValidationPrimaryDuplicate: i18n.FieldContactPrimaryDuplicate, contactaction.ValidationQueryInvalid: i18n.FieldContactQueryInvalid,
	}
	return translateValidationFields(fields, keys)
}
