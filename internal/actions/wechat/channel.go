//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

var (
	// ErrAppIDTaken 表示公众号已连接到本部署同一接入方式的其他渠道。
	ErrAppIDTaken = errors.New("wechat app id connected to another channel")
	// errAppIDChanged 表示渠道已连接其他公众号。
	errAppIDChanged = errors.New("wechat channel connected to another app id")
)

// appIDIndexes 是各公众号渠道类型在部署内保证 AppID 唯一的索引。
var appIDIndexes = map[domain.ChannelType]string{
	domain.ChannelTypeWechatKey:           "channels_wechat_key_app_id_unique",
	domain.ChannelTypeWechatAuthorization: "channels_wechat_authorization_app_id_unique",
}

// CredentialError 表示公众号凭据未通过微信验证。
type CredentialError struct {
	Failure domain.WechatTokenFailure
	// Detail 在白名单失败时是微信识别的调用方 IP，其他失败时是错误码与说明或错误信息。
	Detail string
	Err    error
}

// Error 返回验证失败原因。
func (e *CredentialError) Error() string {
	return fmt.Sprintf("wechat credential rejected: %v", e.Err)
}

// Unwrap 返回微信调用错误。
func (e *CredentialError) Unwrap() error {
	return e.Err
}

// newCredentialError 把微信调用错误归类为凭据验证失败。
func newCredentialError(err error) *CredentialError {
	failure, detail := tokenFailure(err)
	return &CredentialError{Failure: failure, Detail: detail, Err: err}
}

// loadChannelModel 用 query 读取指定企业中 channelType 类型的公众号渠道。
func loadChannelModel(ctx context.Context, query *bun.SelectQuery, channelType domain.ChannelType, workspaceID, channelID string) (*servermodels.Channel, error) {
	channel := &servermodels.Channel{}
	err := query.Model(channel).
		Where("c.id = ?", channelID).
		Where("c.workspace_id = ?", workspaceID).
		Where("c.type = ?", channelType).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wechat channel: %w", err)
	}
	return channel, nil
}

// claimAppID 在事务内为已锁定且尚未连接公众号的渠道登记 appID；渠道已连接其他公众号时返回 errAppIDChanged，appID 已被同类型其他渠道占用时返回 ErrAppIDTaken，渠道已启用且该公众号的另一种接入渠道也已启用时返回 channelaction.ErrWechatAccountEnabledElsewhere。
func claimAppID(ctx context.Context, tx bun.Tx, channel *servermodels.Channel, appID string) error {
	if channel.ProviderAccountID != nil {
		if *channel.ProviderAccountID != appID {
			return errAppIDChanged
		}
		return nil
	}
	// 同类型其他渠道已占用 appID 时先判定为占用：同时违反多个唯一索引时数据库只报告其中一个。
	taken, err := tx.NewSelect().Model((*servermodels.Channel)(nil)).
		Where("c.type = ? AND c.provider_account_id = ? AND c.id <> ?", channel.Type, appID, channel.ID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check wechat app id: %w", err)
	}
	if taken {
		return ErrAppIDTaken
	}
	_, err = tx.NewUpdate().Model((*servermodels.Channel)(nil)).
		Where("id = ?", channel.ID).
		Set("provider_account_id = ?", appID).
		Exec(ctx)
	if pgerr.UniqueViolationOn(err, appIDIndexes[domain.ChannelType(channel.Type)]) {
		return ErrAppIDTaken
	}
	if pgerr.UniqueViolationOn(err, channelaction.WechatEnabledAccountIndex) {
		return channelaction.ErrWechatAccountEnabledElsewhere
	}
	if err != nil {
		return fmt.Errorf("connect wechat channel: %w", err)
	}
	return nil
}
