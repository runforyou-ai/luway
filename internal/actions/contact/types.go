//go:build server

package contact

import (
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// MethodInput 定义联系人联系方式输入。
type MethodInput struct {
	Type      domain.ContactMethodType
	Value     string
	Label     string
	IsPrimary bool
}

// ContactInput 定义外部联系人可编辑字段。
type ContactInput struct {
	DisplayName string
	ChannelID   string
	Stage       domain.ContactStage
	Notes       string
	Methods     []MethodInput
}

// ListInput 定义外部联系人列表查询条件。
type ListInput struct {
	Query      string
	Stage      domain.ContactStage
	ChannelID  string
	MethodType domain.ContactMethodType
	TagID      string
	Sort       domain.ContactSort
	Page       int
	PageSize   int
	Deleted    bool
}

// PageInfo 定义服务端分页信息。
type PageInfo = common.PageInfo

// ContactSummary 定义外部联系人列表项；DisplayName 为成员界面名称，按档案名称、最近更新的渠道身份名称、首选邮箱依次取第一个非空值。
type ContactSummary struct {
	ID                string              `bun:"id" json:"id"`
	Number            int64               `bun:"number" json:"number"`
	DisplayName       *string             `bun:"display_name" json:"displayName"`
	AvatarFileID      *string             `bun:"avatar_file_id" json:"avatarFileId"`
	Stage             domain.ContactStage `bun:"stage" json:"stage"`
	PrimaryEmail      *string             `bun:"primary_email" json:"primaryEmail"`
	PrimaryPhone      *string             `bun:"primary_phone" json:"primaryPhone"`
	SourceChannelName string              `bun:"source_channel_name" json:"sourceChannelName"`
	CreatedAt         time.Time           `bun:"created_at" json:"createdAt"`
	DeletedAt         *time.Time          `bun:"deleted_at" json:"deletedAt"`
	Tags              []TagSummary        `bun:"-" json:"tags"`
}

// TagSummary 定义联系人列表项上的标签。
type TagSummary struct {
	ContactID string `bun:"contact_id" json:"-"`
	ID        string `bun:"id" json:"id"`
	Name      string `bun:"name" json:"name"`
}

// ContactRecord 定义联系人详情字段。
type ContactRecord struct {
	ID              string              `bun:"id" json:"id"`
	Number          int64               `bun:"number" json:"number"`
	SourceChannelID string              `bun:"source_channel_id" json:"sourceChannelId"`
	DisplayName     *string             `bun:"display_name" json:"displayName"`
	Stage           domain.ContactStage `bun:"stage" json:"stage"`
	Notes           *string             `bun:"notes" json:"notes"`
	CreatedAt       time.Time           `bun:"created_at" json:"createdAt"`
}

// ContactMethod 定义联系人详情中的联系方式。
type ContactMethod struct {
	Type      domain.ContactMethodType `bun:"type" json:"type"`
	Value     string                   `bun:"value" json:"value"`
	Label     *string                  `bun:"label" json:"label"`
	IsPrimary bool                     `bun:"is_primary" json:"isPrimary"`
}

// ChannelIdentity 定义联系人渠道身份及渠道摘要。
type ChannelIdentity struct {
	ChannelID   string  `bun:"channel_id" json:"channelId"`
	ChannelName string  `bun:"channel_name" json:"channelName"`
	ExternalID  string  `bun:"external_id" json:"externalId"`
	DisplayName *string `bun:"display_name" json:"displayName"`
}

// SourceChannel 定义联系人来源渠道。
type SourceChannel struct {
	ID   string             `bun:"id" json:"id"`
	Type domain.ChannelType `bun:"type" json:"type"`
	Name string             `bun:"name" json:"name"`
}

// ContactDetail 定义外部联系人完整详情；Name 为成员界面名称，规则与 ContactSummary.DisplayName 一致。
type ContactDetail struct {
	Contact           ContactRecord          `json:"contact"`
	Name              *string                `json:"name"`
	AvatarFileID      *string                `json:"avatarFileId"`
	SourceChannel     SourceChannel          `json:"sourceChannel"`
	Methods           []ContactMethod        `json:"methods"`
	ChannelIdentities []ChannelIdentity      `json:"channelIdentities"`
	Profile           contactprofile.Profile `json:"profile"`
}

// ListOutput 定义外部联系人分页结果。
type ListOutput struct {
	Contacts []ContactSummary `json:"contacts"`
	Page     PageInfo         `json:"page"`
}
