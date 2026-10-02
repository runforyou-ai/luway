//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CustomerProfile 表示客户会话的联系人、当前周期的客户身份与访客上下文。
type CustomerProfile struct {
	ContactID        string
	IdentityVerified bool
	ExternalUserID   string
	Email            string
	VisitorContext   *domain.VisitorContext
}

// GetCustomerProfileQuery 读取客户会话的客户身份与当前周期访客上下文。
type GetCustomerProfileQuery struct {
	db *bun.DB
}

// NewGetCustomerProfileQuery 创建客户资料查询。
func NewGetCustomerProfileQuery(db *bun.DB) *GetCustomerProfileQuery {
	return &GetCustomerProfileQuery{db: db}
}

// Execute 返回当前企业客户会话的客户资料；会话不存在或不是客户会话时返回 ErrNotFound。
func (q *GetCustomerProfileQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (CustomerProfile, error) {
	if !common.ValidUUID(conversationID) {
		return CustomerProfile{}, ErrNotFound
	}
	row := struct {
		ContactID      string                 `bun:"contact_id"`
		VerifiedUserID *string                `bun:"verified_user_id"`
		Email          *string                `bun:"email"`
		VisitorContext *domain.VisitorContext `bun:"visitor_context,type:jsonb"`
	}{}
	err := q.db.NewSelect().
		TableExpr("channel_conversations AS cc").
		ColumnExpr("c.id::text AS contact_id, cci.verified_user_id, ss.visitor_context").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = ? AND cm.is_primary) AS email", domain.ContactMethodTypeEmail).
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Join("JOIN contacts AS c ON c.id = cci.contact_id AND c.organization_id = cci.organization_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = svc.current_service_session_id AND ss.organization_id = cc.organization_id").
		Where("cc.organization_id = ?", identity.Organization.ID).
		Where("cc.conversation_id = ?", conversationID).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return CustomerProfile{}, ErrNotFound
	}
	if err != nil {
		return CustomerProfile{}, fmt.Errorf("get customer profile: %w", err)
	}
	profile := CustomerProfile{ContactID: row.ContactID, VisitorContext: row.VisitorContext}
	if row.VerifiedUserID != nil {
		profile.IdentityVerified, profile.ExternalUserID = true, *row.VerifiedUserID
	}
	if row.Email != nil {
		profile.Email = *row.Email
	}
	return profile, nil
}
