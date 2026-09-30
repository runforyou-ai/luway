//go:build server

package customerchat

import (
	"context"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/uptrace/bun"
)

// WebsiteVisitorAudience 是网站访客事件流的受众标识。
type WebsiteVisitorAudience struct {
	OrganizationID    string
	ChannelID         string
	ChannelIdentityID string
}

// AuthorizeWebsiteVisitorQuery 按启用的网站渠道和访客外部编号解析访客事件流受众。
type AuthorizeWebsiteVisitorQuery struct {
	db *bun.DB
}

// NewAuthorizeWebsiteVisitorQuery 创建网站访客受众解析查询。
func NewAuthorizeWebsiteVisitorQuery(db *bun.DB) *AuthorizeWebsiteVisitorQuery {
	return &AuthorizeWebsiteVisitorQuery{db: db}
}

// Execute 返回当前访客的渠道身份受众；渠道停用或不存在返回 ErrChannelNotFound，尚未建立身份返回 ErrConversationNotFound，读操作不创建联系人。
func (q *AuthorizeWebsiteVisitorQuery) Execute(ctx context.Context, channelID, externalID string) (WebsiteVisitorAudience, error) {
	fields := map[string]conversationaction.ValidationCode{}
	if !common.ValidUUID(channelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(externalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return WebsiteVisitorAudience{}, &conversationaction.ValidationError{Fields: fields}
	}
	channel, err := loadWebsiteChannel(ctx, q.db, channelID)
	if err != nil {
		return WebsiteVisitorAudience{}, err
	}
	identity, found, err := loadWebsiteVisitorIdentity(ctx, q.db, channel, externalID)
	if err != nil {
		return WebsiteVisitorAudience{}, err
	}
	if !found {
		return WebsiteVisitorAudience{}, conversationaction.ErrConversationNotFound
	}
	return WebsiteVisitorAudience{OrganizationID: channel.OrganizationID, ChannelID: channel.ID, ChannelIdentityID: identity.ID}, nil
}
