//go:build server

package direct

import (
	"context"
	"log/slog"

	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// ListContactFields 返回当前企业的联系人字段。
func (o *contactOps) ListContactFields(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ContactFieldList, error) {
	fields, err := o.listContactFields.Execute(ctx, identity)
	if err != nil {
		return appservice.ContactFieldList{}, contactProfileError(meta, err, i18n.ErrorContactFieldListFailed, contactFieldValidationKeys)
	}
	return appservice.ContactFieldList{Fields: arr.Map(fields, contactFieldFromAction)}, nil
}

// CreateContactField 新增联系人字段。
func (o *contactOps) CreateContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactFieldInput) (appservice.ContactField, error) {
	field, err := o.createContactField.Execute(ctx, identity, contactFieldInput(input))
	if err != nil {
		return appservice.ContactField{}, contactProfileError(meta, err, i18n.ErrorContactFieldCreateFailed, contactFieldValidationKeys)
	}
	slog.InfoContext(ctx, "联系人字段已新增", "contact_field_id", field.ID)
	return contactFieldFromAction(*field), nil
}

// UpdateContactField 修改联系人字段。
func (o *contactOps) UpdateContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fieldID string, input appservice.ContactFieldInput) (appservice.ContactField, error) {
	field, err := o.updateContactField.Execute(ctx, identity, fieldID, contactFieldInput(input))
	if err != nil {
		return appservice.ContactField{}, contactProfileError(meta, err, i18n.ErrorContactFieldUpdateFailed, contactFieldValidationKeys)
	}
	slog.InfoContext(ctx, "联系人字段已更新", "contact_field_id", fieldID)
	return contactFieldFromAction(*field), nil
}

// DeleteContactField 删除联系人字段及其全部取值。
func (o *contactOps) DeleteContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fieldID string) error {
	if err := o.deleteContactField.Execute(ctx, identity, fieldID); err != nil {
		return contactProfileError(meta, err, i18n.ErrorContactFieldDeleteFailed, contactFieldValidationKeys)
	}
	slog.InfoContext(ctx, "联系人字段已删除", "contact_field_id", fieldID)
	return nil
}

// ListContactTags 返回当前企业的联系人标签。
func (o *contactOps) ListContactTags(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ContactTagList, error) {
	tags, err := o.listContactTags.Execute(ctx, identity)
	if err != nil {
		return appservice.ContactTagList{}, contactProfileError(meta, err, i18n.ErrorContactTagListFailed, contactTagValidationKeys)
	}
	return appservice.ContactTagList{Tags: arr.Map(tags, func(tag contactprofileaction.Tag) appservice.ContactTag {
		return appservice.ContactTag(tag)
	})}, nil
}

// CreateContactTag 新增联系人标签。
func (o *contactOps) CreateContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactTagInput) (appservice.ContactTag, error) {
	tag, err := o.createContactTag.Execute(ctx, identity, contactprofileaction.TagInput(input))
	if err != nil {
		return appservice.ContactTag{}, contactProfileError(meta, err, i18n.ErrorContactTagCreateFailed, contactTagValidationKeys)
	}
	slog.InfoContext(ctx, "联系人标签已新增", "contact_tag_id", tag.ID)
	return appservice.ContactTag(*tag), nil
}

// UpdateContactTag 修改联系人标签。
func (o *contactOps) UpdateContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, tagID string, input appservice.ContactTagInput) (appservice.ContactTag, error) {
	tag, err := o.updateContactTag.Execute(ctx, identity, tagID, contactprofileaction.TagInput(input))
	if err != nil {
		return appservice.ContactTag{}, contactProfileError(meta, err, i18n.ErrorContactTagUpdateFailed, contactTagValidationKeys)
	}
	slog.InfoContext(ctx, "联系人标签已更新", "contact_tag_id", tagID)
	return appservice.ContactTag(*tag), nil
}

// DeleteContactTag 删除联系人标签并从所有联系人上移除。
func (o *contactOps) DeleteContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, tagID string) error {
	if err := o.deleteContactTag.Execute(ctx, identity, tagID); err != nil {
		return contactProfileError(meta, err, i18n.ErrorContactTagDeleteFailed, contactTagValidationKeys)
	}
	slog.InfoContext(ctx, "联系人标签已删除", "contact_tag_id", tagID)
	return nil
}

// SetContactFieldValue 由客服填写或清空联系人字段。
func (o *contactOps) SetContactFieldValue(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, fieldID string, input appservice.ContactFieldValueInput) error {
	if err := o.setContactFieldValue.Execute(ctx, identity, contactID, fieldID, input.Value); err != nil {
		return contactProfileError(meta, err, i18n.ErrorContactProfileUpdateFailed, contactFieldValidationKeys)
	}
	return nil
}

// AddContactTag 由客服给联系人添加标签。
func (o *contactOps) AddContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, tagID string) error {
	if err := o.addContactTag.Execute(ctx, identity, contactID, tagID); err != nil {
		return contactProfileError(meta, err, i18n.ErrorContactProfileUpdateFailed, contactTagValidationKeys)
	}
	return nil
}

// RemoveContactTag 由客服移除联系人上的标签。
func (o *contactOps) RemoveContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, tagID string) error {
	if err := o.removeContactTag.Execute(ctx, identity, contactID, tagID); err != nil {
		return contactProfileError(meta, err, i18n.ErrorContactProfileUpdateFailed, contactTagValidationKeys)
	}
	return nil
}

// contactFieldValidationKeys 把联系人字段与取值校验错误码映射为本地化文案键。
var contactFieldValidationKeys = map[common.FieldCode]i18n.Key{
	contactprofileaction.ValidationNameDuplicate:      i18n.FieldContactFieldNameDuplicate,
	contactprofileaction.ValidationFieldTypeImmutable: i18n.FieldContactFieldTypeImmutable,
	contactprofileaction.ValidationOptionsRequired:    i18n.FieldContactFieldOptionsRequired,
	contactprofileaction.ValidationOptionInvalid:      i18n.FieldContactFieldOptionInvalid,
	contactprofileaction.ValidationOptionDuplicate:    i18n.FieldContactFieldOptionDuplicate,
	contactprofileaction.ValidationValueInvalid:       i18n.FieldContactFieldValueInvalid,
	contactprofileaction.ValidationValueTooLong:       i18n.FieldContactFieldValueTooLong,
}

// contactTagValidationKeys 把联系人标签校验错误码映射为本地化文案键。
var contactTagValidationKeys = map[common.FieldCode]i18n.Key{
	contactprofileaction.ValidationNameDuplicate: i18n.FieldContactTagNameDuplicate,
}

// contactProfileErrors 是联系人字段、标签与档案操作在字段校验之后匹配的错误。
var contactProfileErrors = dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(contactprofileaction.ErrContactNotFound, dispatch.NotFound(i18n.ErrorContactNotFound)),
	dispatch.Is(contactprofileaction.ErrFieldNotFound, dispatch.NotFound(i18n.ErrorContactFieldNotFound)),
	dispatch.Is(contactprofileaction.ErrTagNotFound, dispatch.NotFound(i18n.ErrorContactTagNotFound)),
	dispatch.Is(contactprofileaction.ErrSyncedFromSignedIdentity, dispatch.Conflict(i18n.ErrorContactProfileSyncedFromSignedIdentity, "contact_profile_synced_from_signed_identity")),
}

// contactProfileError 把联系人字段、标签与档案操作错误转换为结构化、本地化错误。
func contactProfileError(meta appservice.RequestMeta, err error, failureKey i18n.Key, validationKeys map[common.FieldCode]i18n.Key) error {
	return dispatch.Catalog{dispatch.FieldRule(validationKeys), contactProfileErrors.Find}.Translate(meta, err, failureKey)
}

// contactFieldInput 把联系人字段契约输入转换为动作层输入。
func contactFieldInput(input appservice.ContactFieldInput) contactprofileaction.FieldInput {
	options := arr.OrEmpty(arr.Map(input.Options, func(option appservice.ContactFieldOption) domain.ContactFieldOption {
		return domain.ContactFieldOption(option)
	}))
	return contactprofileaction.FieldInput{Name: input.Name, Type: input.Type, Options: options, AIInstruction: input.AIInstruction}
}

// contactFieldFromAction 转换联系人字段契约。
func contactFieldFromAction(field contactprofileaction.Field) appservice.ContactField {
	options := arr.Map(field.Options, func(option domain.ContactFieldOption) appservice.ContactFieldOption {
		return appservice.ContactFieldOption(option)
	})
	return appservice.ContactField{
		ID: field.ID, Name: field.Name, Type: field.Type, Options: options, AIInstruction: field.AIInstruction,
		CreatedAt: field.CreatedAt, UpdatedAt: field.UpdatedAt,
	}
}

// contactProfileFromAction 转换联系人档案契约。
func contactProfileFromAction(profile contactprofileaction.Profile) appservice.ContactProfile {
	return appservice.ContactProfile{
		Fields: arr.Map(profile.Fields, func(value contactprofileaction.FieldValue) appservice.ContactFieldValue {
			return appservice.ContactFieldValue{
				FieldID: value.FieldID, Value: value.Value, Source: value.Source,
				SourceSession: (*appservice.ContactProfileSourceSession)(value.SourceSession), UpdatedAt: value.UpdatedAt,
			}
		}),
		Tags: arr.Map(profile.Tags, func(tag contactprofileaction.AssignedTag) appservice.ContactAssignedTag {
			return appservice.ContactAssignedTag{
				ID: tag.ID, Name: tag.Name, Source: tag.Source, SourceSession: (*appservice.ContactProfileSourceSession)(tag.SourceSession),
			}
		}),
	}
}
