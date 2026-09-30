//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	contactprofileaction "github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// ListContactFields 返回当前企业的联系人字段。
func (o *directOperations) ListContactFields(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ContactFieldList, error) {
	fields, err := o.listContactFields.Execute(ctx, identity)
	if err != nil {
		return appservice.ContactFieldList{}, contactProfileError(ctx, meta, err, i18n.ErrorContactFieldListFailed, contactFieldValidationKeys)
	}
	result := appservice.ContactFieldList{Fields: make([]appservice.ContactField, 0, len(fields))}
	for _, field := range fields {
		result.Fields = append(result.Fields, contactFieldFromAction(field))
	}
	return result, nil
}

// CreateContactField 新增联系人字段。
func (o *directOperations) CreateContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactFieldInput) (appservice.ContactField, error) {
	field, err := o.createContactField.Execute(ctx, identity, contactFieldInput(input))
	if err != nil {
		return appservice.ContactField{}, contactProfileError(ctx, meta, err, i18n.ErrorContactFieldCreateFailed, contactFieldValidationKeys)
	}
	slog.Info("联系人字段已新增", "organization_id", identity.Organization.ID, "contact_field_id", field.ID)
	return contactFieldFromAction(*field), nil
}

// UpdateContactField 修改联系人字段。
func (o *directOperations) UpdateContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fieldID string, input appservice.ContactFieldInput) (appservice.ContactField, error) {
	field, err := o.updateContactField.Execute(ctx, identity, fieldID, contactFieldInput(input))
	if err != nil {
		return appservice.ContactField{}, contactProfileError(ctx, meta, err, i18n.ErrorContactFieldUpdateFailed, contactFieldValidationKeys)
	}
	slog.Info("联系人字段已更新", "organization_id", identity.Organization.ID, "contact_field_id", fieldID)
	return contactFieldFromAction(*field), nil
}

// DeleteContactField 删除联系人字段及其全部取值。
func (o *directOperations) DeleteContactField(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fieldID string) error {
	if err := o.deleteContactField.Execute(ctx, identity, fieldID); err != nil {
		return contactProfileError(ctx, meta, err, i18n.ErrorContactFieldDeleteFailed, contactFieldValidationKeys)
	}
	slog.Info("联系人字段已删除", "organization_id", identity.Organization.ID, "contact_field_id", fieldID)
	return nil
}

// ListContactTags 返回当前企业的联系人标签。
func (o *directOperations) ListContactTags(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ContactTagList, error) {
	tags, err := o.listContactTags.Execute(ctx, identity)
	if err != nil {
		return appservice.ContactTagList{}, contactProfileError(ctx, meta, err, i18n.ErrorContactTagListFailed, contactTagValidationKeys)
	}
	result := appservice.ContactTagList{Tags: make([]appservice.ContactTag, 0, len(tags))}
	for _, tag := range tags {
		result.Tags = append(result.Tags, appservice.ContactTag(tag))
	}
	return result, nil
}

// CreateContactTag 新增联系人标签。
func (o *directOperations) CreateContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ContactTagInput) (appservice.ContactTag, error) {
	tag, err := o.createContactTag.Execute(ctx, identity, contactprofileaction.TagInput(input))
	if err != nil {
		return appservice.ContactTag{}, contactProfileError(ctx, meta, err, i18n.ErrorContactTagCreateFailed, contactTagValidationKeys)
	}
	slog.Info("联系人标签已新增", "organization_id", identity.Organization.ID, "contact_tag_id", tag.ID)
	return appservice.ContactTag(*tag), nil
}

// UpdateContactTag 修改联系人标签。
func (o *directOperations) UpdateContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, tagID string, input appservice.ContactTagInput) (appservice.ContactTag, error) {
	tag, err := o.updateContactTag.Execute(ctx, identity, tagID, contactprofileaction.TagInput(input))
	if err != nil {
		return appservice.ContactTag{}, contactProfileError(ctx, meta, err, i18n.ErrorContactTagUpdateFailed, contactTagValidationKeys)
	}
	slog.Info("联系人标签已更新", "organization_id", identity.Organization.ID, "contact_tag_id", tagID)
	return appservice.ContactTag(*tag), nil
}

// DeleteContactTag 删除联系人标签并从所有联系人上移除。
func (o *directOperations) DeleteContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, tagID string) error {
	if err := o.deleteContactTag.Execute(ctx, identity, tagID); err != nil {
		return contactProfileError(ctx, meta, err, i18n.ErrorContactTagDeleteFailed, contactTagValidationKeys)
	}
	slog.Info("联系人标签已删除", "organization_id", identity.Organization.ID, "contact_tag_id", tagID)
	return nil
}

// SetContactFieldValue 由客服填写或清空联系人字段。
func (o *directOperations) SetContactFieldValue(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, fieldID string, input appservice.ContactFieldValueInput) error {
	if err := o.setContactFieldValue.Execute(ctx, identity, contactID, fieldID, input.Value); err != nil {
		return contactProfileError(ctx, meta, err, i18n.ErrorContactProfileUpdateFailed, contactFieldValidationKeys)
	}
	return nil
}

// AddContactTag 由客服给联系人添加标签。
func (o *directOperations) AddContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, tagID string) error {
	if err := o.addContactTag.Execute(ctx, identity, contactID, tagID); err != nil {
		return contactProfileError(ctx, meta, err, i18n.ErrorContactProfileUpdateFailed, contactTagValidationKeys)
	}
	return nil
}

// RemoveContactTag 由客服移除联系人上的标签。
func (o *directOperations) RemoveContactTag(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, contactID, tagID string) error {
	if err := o.removeContactTag.Execute(ctx, identity, contactID, tagID); err != nil {
		return contactProfileError(ctx, meta, err, i18n.ErrorContactProfileUpdateFailed, contactTagValidationKeys)
	}
	return nil
}

// contactFieldValidationKeys 把联系人字段与取值校验错误码映射为本地化文案键。
var contactFieldValidationKeys = map[common.FieldCode]i18n.Key{
	contactprofileaction.ValidationNameRequired:         i18n.FieldContactFieldNameRequired,
	contactprofileaction.ValidationNameTooLong:          i18n.FieldContactFieldNameTooLong,
	contactprofileaction.ValidationNameDuplicate:        i18n.FieldContactFieldNameDuplicate,
	contactprofileaction.ValidationFieldTypeInvalid:     i18n.FieldContactFieldTypeInvalid,
	contactprofileaction.ValidationFieldTypeImmutable:   i18n.FieldContactFieldTypeImmutable,
	contactprofileaction.ValidationOptionsRequired:      i18n.FieldContactFieldOptionsRequired,
	contactprofileaction.ValidationOptionInvalid:        i18n.FieldContactFieldOptionInvalid,
	contactprofileaction.ValidationOptionDuplicate:      i18n.FieldContactFieldOptionDuplicate,
	contactprofileaction.ValidationValueInvalid:         i18n.FieldContactFieldValueInvalid,
	contactprofileaction.ValidationValueTooLong:         i18n.FieldContactFieldValueTooLong,
	contactprofileaction.ValidationAIInstructionTooLong: i18n.FieldContactFieldAIInstructionTooLong,
}

// contactTagValidationKeys 把联系人标签校验错误码映射为本地化文案键。
var contactTagValidationKeys = map[common.FieldCode]i18n.Key{
	contactprofileaction.ValidationNameRequired:         i18n.FieldContactTagNameRequired,
	contactprofileaction.ValidationNameTooLong:          i18n.FieldContactTagNameTooLong,
	contactprofileaction.ValidationNameDuplicate:        i18n.FieldContactTagNameDuplicate,
	contactprofileaction.ValidationAIInstructionTooLong: i18n.FieldContactTagAIInstructionTooLong,
}

// contactProfileError 把联系人字段、标签与档案操作错误转换为结构化、本地化错误。
func contactProfileError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, validationKeys map[common.FieldCode]i18n.Key) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, validationKeys))
	}
	switch {
	case errors.Is(err, identityaction.ErrInvalid):
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	case errors.Is(err, contactprofileaction.ErrContactNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorContactNotFound)
	case errors.Is(err, contactprofileaction.ErrFieldNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorContactFieldNotFound)
	case errors.Is(err, contactprofileaction.ErrTagNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorContactTagNotFound)
	case errors.Is(err, contactprofileaction.ErrSyncedFromWebsite):
		return appservice.ConflictError(meta, i18n.ErrorContactProfileSyncedFromWebsite, "contact_profile_synced_from_website")
	}
	slog.Warn("联系人档案操作失败", "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
}

// contactFieldInput 把联系人字段契约输入转换为动作层输入。
func contactFieldInput(input appservice.ContactFieldInput) contactprofileaction.FieldInput {
	options := make([]domain.ContactFieldOption, 0, len(input.Options))
	for _, option := range input.Options {
		options = append(options, domain.ContactFieldOption(option))
	}
	return contactprofileaction.FieldInput{Name: input.Name, Type: domain.ContactFieldType(input.Type), Options: options, AIInstruction: input.AIInstruction}
}

// contactFieldFromAction 转换联系人字段契约。
func contactFieldFromAction(field contactprofileaction.Field) appservice.ContactField {
	options := make([]appservice.ContactFieldOption, 0, len(field.Options))
	for _, option := range field.Options {
		options = append(options, appservice.ContactFieldOption(option))
	}
	return appservice.ContactField{
		ID: field.ID, Name: field.Name, Type: appservice.ContactFieldType(field.Type), Options: options, AIInstruction: field.AIInstruction,
		CreatedAt: field.CreatedAt, UpdatedAt: field.UpdatedAt,
	}
}

// contactProfileFromAction 转换联系人档案契约。
func contactProfileFromAction(profile contactprofileaction.Profile) appservice.ContactProfile {
	result := appservice.ContactProfile{Fields: make([]appservice.ContactFieldValue, 0, len(profile.Fields)), Tags: make([]appservice.ContactAssignedTag, 0, len(profile.Tags))}
	for _, value := range profile.Fields {
		result.Fields = append(result.Fields, appservice.ContactFieldValue{
			FieldID: value.FieldID, Value: value.Value, Source: appservice.ContactProfileSource(value.Source),
			SourceSession: (*appservice.ContactProfileSourceSession)(value.SourceSession), UpdatedAt: value.UpdatedAt,
		})
	}
	for _, tag := range profile.Tags {
		result.Tags = append(result.Tags, appservice.ContactAssignedTag{
			ID: tag.ID, Name: tag.Name, Source: appservice.ContactProfileSource(tag.Source), SourceSession: (*appservice.ContactProfileSourceSession)(tag.SourceSession),
		})
	}
	return result
}
