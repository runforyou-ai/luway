//go:build server

package customerchat

import (
	"context"
	"errors"
	"fmt"
	"time"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// VerifiedWebsiteCustomer 是验签通过的网站登录用户及其所属企业。
type VerifiedWebsiteCustomer struct {
	WorkspaceID string
	Customer    SignedCustomer
	ExpiresAt   time.Time
}

// VerifyWebsiteCustomerQuery 按企业客户身份密钥校验网站登录用户签名身份。
type VerifyWebsiteCustomerQuery struct {
	db *bun.DB
}

// NewVerifyWebsiteCustomerQuery 创建网站登录用户签名身份校验查询。
func NewVerifyWebsiteCustomerQuery(db *bun.DB) *VerifyWebsiteCustomerQuery {
	return &VerifyWebsiteCustomerQuery{db: db}
}

// Execute 按接待客户的网站渠道所属企业校验签名身份；渠道停用、所属工作区已暂停或渠道不存在返回 ErrChannelNotFound，签名无效返回 ErrCustomerIdentityInvalid。
func (q *VerifyWebsiteCustomerQuery) Execute(ctx context.Context, channelID, token string) (VerifiedWebsiteCustomer, error) {
	if !str.IsUUID(channelID) {
		return VerifiedWebsiteCustomer{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"channelId": ValidationChannelIDInvalid}}
	}
	channel, err := loadWebsiteChannel(ctx, q.db, channelID)
	if err != nil {
		return VerifiedWebsiteCustomer{}, err
	}
	return q.ExecuteForWorkspace(ctx, channel.WorkspaceID, token)
}

// ExecuteForWorkspace 按指定企业的客户身份密钥校验签名身份，企业尚未生成密钥时签名一律无效。
func (q *VerifyWebsiteCustomerQuery) ExecuteForWorkspace(ctx context.Context, workspaceID, token string) (VerifiedWebsiteCustomer, error) {
	customer, expiresAt, err := VerifyCustomerToken(ctx, q.db, workspaceID, token)
	if err != nil {
		return VerifiedWebsiteCustomer{}, err
	}
	return VerifiedWebsiteCustomer{WorkspaceID: workspaceID, Customer: customer, ExpiresAt: expiresAt}, nil
}

// VerifyCustomerToken 按企业客户身份密钥校验签名身份并返回签名客户与过期时间；企业尚未生成密钥或签名无效时返回 ErrCustomerIdentityInvalid。
func VerifyCustomerToken(ctx context.Context, db bun.IDB, workspaceID, token string) (SignedCustomer, time.Time, error) {
	secret, err := customerserviceaction.LoadCustomerIdentitySecret(ctx, db, workspaceID)
	if err != nil {
		return SignedCustomer{}, time.Time{}, err
	}
	if secret == "" {
		return SignedCustomer{}, time.Time{}, fmt.Errorf("%w: secret is not generated", ErrCustomerIdentityInvalid)
	}
	// 身份令牌由客户网站签发，按本机时钟校验有效期。
	claims, err := customeridentity.Verify(secret, token, time.Now()) //clock:local
	if errors.Is(err, customeridentity.ErrInvalid) {
		return SignedCustomer{}, time.Time{}, fmt.Errorf("%w: %w", ErrCustomerIdentityInvalid, err)
	}
	if err != nil {
		return SignedCustomer{}, time.Time{}, err
	}
	return SignedCustomer{
		UserID: claims.UserID, Name: claims.Name, Email: claims.Email, ChannelID: claims.ChannelID, ExternalID: claims.ExternalID,
		Profile: domain.SignedContactProfile{Attributes: claims.Attributes, Tags: claims.Tags},
	}, claims.ExpiresAt, nil
}
