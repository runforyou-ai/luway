//go:build server

// Package contactprofile 实现企业联系人字段与标签的定义维护，以及联系人档案的读取和编辑。
package contactprofile

import (
	"errors"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

var (
	// ErrFieldNotFound 表示当前企业中不存在指定联系人字段。
	ErrFieldNotFound = errors.New("contact field not found")
	// ErrTagNotFound 表示当前企业中不存在指定联系人标签。
	ErrTagNotFound = errors.New("contact tag not found")
	// ErrContactNotFound 表示当前企业中不存在指定的未删除联系人。
	ErrContactNotFound = errors.New("contact not found")
	// ErrSyncedFromWebsite 表示字段取值或标签由网站登录信息同步，客服不能修改。
	ErrSyncedFromWebsite = errors.New("contact profile synced from website")
)

const (
	ValidationNameRequired         common.FieldCode = "CONTACT_PROFILE_NAME_REQUIRED"
	ValidationNameTooLong          common.FieldCode = "CONTACT_PROFILE_NAME_TOO_LONG"
	ValidationNameDuplicate        common.FieldCode = "CONTACT_PROFILE_NAME_DUPLICATE"
	ValidationFieldTypeInvalid     common.FieldCode = "CONTACT_FIELD_TYPE_INVALID"
	ValidationFieldTypeImmutable   common.FieldCode = "CONTACT_FIELD_TYPE_IMMUTABLE"
	ValidationOptionsRequired      common.FieldCode = "CONTACT_FIELD_OPTIONS_REQUIRED"
	ValidationOptionInvalid        common.FieldCode = "CONTACT_FIELD_OPTION_INVALID"
	ValidationOptionDuplicate      common.FieldCode = "CONTACT_FIELD_OPTION_DUPLICATE"
	ValidationValueInvalid         common.FieldCode = "CONTACT_FIELD_VALUE_INVALID"
	ValidationValueTooLong         common.FieldCode = "CONTACT_FIELD_VALUE_TOO_LONG"
	ValidationAIInstructionTooLong common.FieldCode = "CONTACT_PROFILE_AI_INSTRUCTION_TOO_LONG"
)

// FieldInput 定义联系人字段可编辑内容；选项编号为空表示新增选项，AI 填写说明为空表示 AI 不填写。
type FieldInput struct {
	Name          string
	Type          domain.ContactFieldType
	Options       []domain.ContactFieldOption
	AIInstruction string
}

// Field 定义联系人字段。
type Field struct {
	ID            string                      `bun:"id"`
	Name          string                      `bun:"name"`
	Type          domain.ContactFieldType     `bun:"type"`
	Options       []domain.ContactFieldOption `bun:"options,type:jsonb"`
	AIInstruction string                      `bun:"ai_instruction"`
	CreatedAt     time.Time                   `bun:"created_at"`
	UpdatedAt     time.Time                   `bun:"updated_at"`
}

// TagInput 定义联系人标签可编辑内容；AI 添加条件为空表示只能由客服添加。
type TagInput struct {
	Name          string
	AIInstruction string
}

// Tag 定义联系人标签。
type Tag struct {
	ID            string    `bun:"id"`
	Name          string    `bun:"name"`
	AIInstruction string    `bun:"ai_instruction"`
	CreatedAt     time.Time `bun:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at"`
}

// ProfileSourceSession 定义 AI 写入档案所依据的客服周期在会话中的位置。
type ProfileSourceSession struct {
	ConversationID   string `json:"conversationId"`
	OpeningMessageID string `json:"openingMessageId"`
}

// FieldValue 定义联系人的一个字段取值；SourceSession 只在来源为 AI 时存在。
type FieldValue struct {
	FieldID       string                      `bun:"field_id"`
	Value         string                      `bun:"value"`
	Source        domain.ContactProfileSource `bun:"source"`
	SourceSession *ProfileSourceSession       `bun:"source_session,type:jsonb"`
	UpdatedAt     time.Time                   `bun:"updated_at"`
}

// AssignedTag 定义联系人上的一个标签；SourceSession 只在来源为 AI 时存在。
type AssignedTag struct {
	ID            string                      `bun:"id"`
	Name          string                      `bun:"name"`
	Source        domain.ContactProfileSource `bun:"source"`
	SourceSession *ProfileSourceSession       `bun:"source_session,type:jsonb"`
}

// Profile 定义联系人档案：字段取值按字段创建顺序排列，标签按名称排列。
type Profile struct {
	Fields []FieldValue
	Tags   []AssignedTag
}
