//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
	"uuid"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// maxAssertedExternalIDLength 是渠道身份断言中外部编号的最大字符数。
const maxAssertedExternalIDLength = 256

// AssertChannelIdentityAction 按业务系统签名的渠道身份断言设定或撤销渠道身份的核验用户。
type AssertChannelIdentityAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewAssertChannelIdentityAction 创建渠道身份断言操作。
func NewAssertChannelIdentityAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *AssertChannelIdentityAction {
	return &AssertChannelIdentityAction{db: db, enqueuer: enqueuer}
}

// Verify 把渠道身份核验为签名中的企业用户：渠道身份不存在时创建，所属联系人按企业用户编号归并，签名档案写入联系人，签名名称写入联系人显示名称；签名必须由渠道所属工作区的客户身份密钥签发，并以 channel_id 与 external_id 声明绑定本次的渠道与外部编号；渠道须经外部平台投递、正在接待客户，且入站消息不自带核验结论。
func (a *AssertChannelIdentityAction) Verify(ctx context.Context, channelID, externalID, token string) error {
	if err := validateAssertionTarget(channelID, externalID); err != nil {
		return err
	}
	var userID string
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, channelinbound.RetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		channel, customer, err := verifyChannelIdentityAssertion(ctx, tx, channelID, externalID, token)
		if err != nil {
			return err
		}
		userID = customer.UserID
		input := contactaction.EnsureChannelIdentityInput{
			WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, ExternalID: externalID,
			ContactID: uuid.NewV7().String(), IdentityID: uuid.NewV7().String(),
			VerifiedUserID: customer.UserID, Email: customer.Email,
		}
		ensured, err := contactaction.EnsureChannelIdentity(ctx, tx, input)
		if err != nil {
			return err
		}
		if ensured, err = contactaction.SyncChannelIdentity(ctx, tx, a.enqueuer, input, ensured); err != nil {
			return err
		}
		if err := contactprofileaction.ApplySignedProfile(ctx, tx, channel.WorkspaceID, ensured.Contact.ID, customer.Profile); err != nil {
			return err
		}
		// 签名名称是业务系统用户的名称，写入联系人显示名称并通知其会话刷新，省略时不改动。
		contact := ensured.Contact
		if customer.Name != "" && (contact.DisplayName == nil || *contact.DisplayName != customer.Name) {
			if _, err := tx.NewUpdate().Model(contact).Set("display_name = ?", customer.Name).
				WherePK().Where("workspace_id = ?", channel.WorkspaceID).Exec(ctx); err != nil {
				return fmt.Errorf("update asserted contact display name: %w", err)
			}
			return chatstate.TouchContactProfileConversations(ctx, tx, channel.WorkspaceID, contact.ID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "渠道身份已按断言核验", "channel_id", channelID, "verified_user_id", userID)
	return nil
}

// Revoke 撤销签名中企业用户对渠道身份的核验；渠道身份不存在或当前未核验为该用户时不做改动。
func (a *AssertChannelIdentityAction) Revoke(ctx context.Context, channelID, externalID, token string) error {
	if err := validateAssertionTarget(channelID, externalID); err != nil {
		return err
	}
	revoked := false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		channel, customer, err := verifyChannelIdentityAssertion(ctx, tx, channelID, externalID, token)
		if err != nil {
			return err
		}
		identity := &servermodels.ChannelIdentity{}
		err = tx.NewSelect().Model(identity).
			Where("ci.workspace_id = ? AND ci.channel_id = ? AND ci.external_id = ?", channel.WorkspaceID, channel.ID, externalID).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find asserted channel identity: %w", err)
		}
		if identity.VerifiedUserID == nil || *identity.VerifiedUserID != customer.UserID {
			return nil
		}
		contact := &servermodels.Contact{}
		if err := tx.NewSelect().Model(contact).Where("ct.workspace_id = ? AND ct.id = ?", channel.WorkspaceID, identity.ContactID).Scan(ctx); err != nil {
			return fmt.Errorf("load asserted channel identity contact: %w", err)
		}
		input := contactaction.EnsureChannelIdentityInput{WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, ExternalID: externalID}
		if _, err := contactaction.SyncChannelIdentity(ctx, tx, a.enqueuer, input, contactaction.EnsuredChannelIdentity{Contact: contact, Identity: identity}); err != nil {
			return err
		}
		revoked = true
		return nil
	})
	if err != nil {
		return err
	}
	if revoked {
		slog.InfoContext(ctx, "渠道身份核验已按断言撤销", "channel_id", channelID)
	}
	return nil
}

// validateAssertionTarget 校验断言路径中的渠道编号与外部编号格式。
func validateAssertionTarget(channelID, externalID string) error {
	fields := map[string]conversationaction.ValidationCode{}
	if !str.IsUUID(channelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if strings.TrimSpace(externalID) != externalID || externalID == "" || utf8.RuneCountInString(externalID) > maxAssertedExternalIDLength {
		fields["externalId"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return &conversationaction.ValidationError{Fields: fields}
	}
	return nil
}

// verifyChannelIdentityAssertion 对渠道取共享锁并按所属工作区的客户身份密钥校验断言；渠道不存在、不经外部平台投递、未在接待客户或以业务系统转发接入时返回 ErrNotFound，签名无效或绑定目标不一致时返回 ErrCustomerIdentityInvalid。
func verifyChannelIdentityAssertion(ctx context.Context, tx bun.Tx, channelID, externalID, token string) (*servermodels.Channel, SignedCustomer, error) {
	channel := &servermodels.Channel{}
	err := tx.NewSelect().Model(channel).
		Where("c.id = ? AND c.type IN (?)", channelID, bun.List(domain.ChannelTypesWith(domain.ChannelCapabilities.ViaPlatform))).
		Where(channelaction.AcceptsCustomersCondition("c")).
		// 业务系统转发的入站消息自带核验结论，不接受断言。
		Where("NOT EXISTS (SELECT 1 FROM telegram_channel_settings AS tcs WHERE tcs.channel_id = c.id AND tcs.connection_mode = ?)", domain.TelegramConnectionGateway).
		For("SHARE OF c").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, SignedCustomer{}, channelaction.ErrNotFound
	}
	if err != nil {
		return nil, SignedCustomer{}, fmt.Errorf("load asserted channel: %w", err)
	}
	customer, _, err := VerifyCustomerToken(ctx, tx, channel.WorkspaceID, token)
	if err != nil {
		return nil, SignedCustomer{}, err
	}
	if !strings.EqualFold(customer.ChannelID, channel.ID) || customer.ExternalID != externalID {
		return nil, SignedCustomer{}, fmt.Errorf("%w: assertion target mismatch", ErrCustomerIdentityInvalid)
	}
	return channel, customer, nil
}
