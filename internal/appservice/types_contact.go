package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ContactStage 表示联系人阶段。
type ContactStage = domain.ContactStage

// ContactMethodType 表示联系人联系方式类型。
type ContactMethodType = domain.ContactMethodType

// ContactSort 表示联系人列表排序方式。
type ContactSort = domain.ContactSort

// ContactMethodInput 定义联系人联系方式输入。
type ContactMethodInput struct {
	Type      ContactMethodType `json:"type"`
	Value     string            `json:"value"`
	Label     string            `json:"label"`
	IsPrimary bool              `json:"isPrimary"`
}

// ContactInput 定义联系人可编辑字段。
type ContactInput struct {
	DisplayName string               `json:"displayName" validate:"max=200" msg:"field.contact_name_too_long"`
	ChannelID   string               `json:"channelId" validate:"notblank,uuid" msg:"notblank=field.contact_channel_required,uuid=field.contact_channel_invalid"`
	Stage       ContactStage         `json:"stage" validate:"oneof=visitor lead customer" msg:"field.contact_stage_invalid"`
	Notes       string               `json:"notes" validate:"max=5000" msg:"field.contact_notes_too_long"`
	Methods     []ContactMethodInput `json:"methods" validate:"max=20" msg:"field.contact_methods_too_many"`
}

// ContactListInput 定义联系人列表查询条件。
type ContactListInput struct {
	Query      string             `json:"query" query:"query"`
	Stage      *ContactStage      `json:"stage,omitempty" query:"stage" validate:"omitnil,oneof=visitor lead customer" msg:"field.contact_stage_invalid"`
	ChannelID  string             `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"field.contact_query_invalid"`
	MethodType *ContactMethodType `json:"methodType,omitempty" query:"methodType" validate:"omitnil,oneof=email phone" msg:"field.contact_query_invalid"`
	TagID      string             `json:"tagId" query:"tagId" validate:"omitempty,uuid" msg:"field.contact_query_invalid"`
	Sort       ContactSort        `json:"sort" query:"sort" validate:"omitempty,oneof=updatedAt.desc createdAt.desc displayName.asc" msg:"field.contact_query_invalid"`
	Page       int                `json:"page" query:"page,default=1"`
	PageSize   int                `json:"pageSize" query:"pageSize,default=50" validate:"max=100" msg:"field.contact_query_invalid"`
	Deleted    bool               `json:"deleted" query:"deleted"` // 为真时列出回收站中的联系人。
}

// ContactSummary 定义联系人列表项；DisplayName 为成员界面名称，按档案名称、最近更新的渠道身份名称、首选邮箱依次取第一个非空值。AvatarURL 为最近更新且带头像的渠道身份头像。
type ContactSummary struct {
	ID                string              `json:"id"`
	Number            int64               `json:"number"`
	DisplayName       *string             `json:"displayName"`
	AvatarURL         string              `json:"avatarUrl"`
	Stage             ContactStage        `json:"stage"`
	PrimaryEmail      *string             `json:"primaryEmail"`
	PrimaryPhone      *string             `json:"primaryPhone"`
	SourceChannelName string              `json:"sourceChannelName"`
	CreatedAt         time.Time           `json:"createdAt"`
	DeletedAt         *time.Time          `json:"deletedAt"`
	Tags              []ContactTagSummary `json:"tags"`
}

// ContactTagSummary 定义联系人上的标签名称。
type ContactTagSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ContactRecord 定义联系人详情字段。
type ContactRecord struct {
	ID              string       `json:"id"`
	Number          int64        `json:"number"`
	SourceChannelID string       `json:"sourceChannelId"`
	DisplayName     *string      `json:"displayName"`
	Stage           ContactStage `json:"stage"`
	Notes           *string      `json:"notes"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// ContactMethod 定义联系人联系方式。
type ContactMethod struct {
	Type      ContactMethodType `json:"type"`
	Value     string            `json:"value"`
	Label     *string           `json:"label"`
	IsPrimary bool              `json:"isPrimary"`
}

// ContactChannelIdentity 定义联系人渠道身份。
type ContactChannelIdentity struct {
	ChannelID   string  `json:"channelId"`
	ChannelName string  `json:"channelName"`
	ExternalID  string  `json:"externalId"`
	DisplayName *string `json:"displayName"`
}

// ContactSourceChannel 定义联系人来源渠道。
type ContactSourceChannel struct {
	ID   string      `json:"id"`
	Type ChannelType `json:"type"`
	Name string      `json:"name"`
}

// Contact 定义联系人完整详情。Name 为成员界面名称，规则与 ContactSummary.DisplayName 一致；AvatarURL 为最近更新且带头像的渠道身份头像。
type Contact struct {
	Contact           ContactRecord            `json:"contact"`
	Name              *string                  `json:"name"`
	AvatarURL         string                   `json:"avatarUrl"`
	SourceChannel     ContactSourceChannel     `json:"sourceChannel"`
	Methods           []ContactMethod          `json:"methods"`
	ChannelIdentities []ContactChannelIdentity `json:"channelIdentities"`
	Profile           ContactProfile           `json:"profile"`
}

// ContactList 定义联系人分页结果。
type ContactList struct {
	Contacts []ContactSummary `json:"contacts"`
	Page     PageInfo         `json:"page"`
}

// ContactFieldType 表示联系人字段类型。
type ContactFieldType = domain.ContactFieldType

// ContactProfileSource 表示联系人字段取值和标签的来源。
type ContactProfileSource = domain.ContactProfileSource

// ContactFieldOption 定义单选字段的选项；新增选项时编号为空。
type ContactFieldOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ContactFieldInput 定义联系人字段可编辑内容；类型创建后不可修改，选项只用于单选字段，AI 填写说明为空表示 AI 不填写。
type ContactFieldInput struct {
	Name          string               `json:"name" validate:"notblank,max=50" msg:"notblank=field.contact_field_name_required,max=field.contact_field_name_too_long"`
	Type          ContactFieldType     `json:"type" validate:"oneof=text number date select" msg:"field.contact_field_type_invalid"`
	Options       []ContactFieldOption `json:"options"`
	AIInstruction string               `json:"aiInstruction" validate:"max=500" msg:"field.contact_field_ai_instruction_too_long"`
}

// ContactField 定义企业自定义的联系人字段；AI 填写说明为空表示 AI 不填写。
type ContactField struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Type          ContactFieldType     `json:"type"`
	Options       []ContactFieldOption `json:"options"`
	AIInstruction string               `json:"aiInstruction"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
}

// ContactFieldList 定义企业联系人字段，按创建顺序排列。
type ContactFieldList struct {
	Fields []ContactField `json:"fields"`
}

// ContactTagInput 定义联系人标签可编辑内容；AI 添加条件为空表示只能由客服添加。
type ContactTagInput struct {
	Name          string `json:"name" validate:"notblank,max=30" msg:"notblank=field.contact_tag_name_required,max=field.contact_tag_name_too_long"`
	AIInstruction string `json:"aiInstruction" validate:"max=500" msg:"field.contact_tag_ai_instruction_too_long"`
}

// ContactTag 定义企业自定义的联系人标签；AI 添加条件为空表示只能由客服添加。
type ContactTag struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	AIInstruction string    `json:"aiInstruction"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ContactTagList 定义企业联系人标签，按名称排列。
type ContactTagList struct {
	Tags []ContactTag `json:"tags"`
}

// ContactFieldValueInput 定义联系人字段取值；数字为十进制文本，日期为 YYYY-MM-DD，单选为选项编号，空值表示清空。
type ContactFieldValueInput struct {
	Value string `json:"value"`
}

// ContactProfileSourceSession 定义 AI 写入档案所依据的客服周期在会话中的位置。
type ContactProfileSourceSession struct {
	ConversationID   string `json:"conversationId"`
	OpeningMessageID string `json:"openingMessageId"`
}

// ContactFieldValue 定义联系人的一个字段取值；SourceSession 只在来源为 AI 时存在。
type ContactFieldValue struct {
	FieldID       string                       `json:"fieldId"`
	Value         string                       `json:"value"`
	Source        ContactProfileSource         `json:"source"`
	SourceSession *ContactProfileSourceSession `json:"sourceSession"`
	UpdatedAt     time.Time                    `json:"updatedAt"`
}

// ContactAssignedTag 定义联系人上的一个标签；SourceSession 只在来源为 AI 时存在。
type ContactAssignedTag struct {
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	Source        ContactProfileSource         `json:"source"`
	SourceSession *ContactProfileSourceSession `json:"sourceSession"`
}

// ContactProfile 定义联系人档案：字段取值按字段创建顺序排列，标签按名称排列。
type ContactProfile struct {
	Fields []ContactFieldValue  `json:"fields"`
	Tags   []ContactAssignedTag `json:"tags"`
}
