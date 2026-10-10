//go:build server

package contact

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetContactQuery 读取当前企业的联系人详情。
type GetContactQuery struct {
	db *bun.DB
}

// NewGetContactQuery 创建联系人详情查询。
func NewGetContactQuery(db *bun.DB) *GetContactQuery {
	return &GetContactQuery{db: db}
}

// Execute 返回当前企业中未删除的联系人详情。
func (q *GetContactQuery) Execute(ctx context.Context, identity *servermodels.Identity, contactID string) (*ContactDetail, error) {
	return loadContactDetail(ctx, q.db, identity.Workspace.ID, contactID)
}

// loadContactDetail 读取联系人详情及其联系方式、渠道身份和档案。
func loadContactDetail(ctx context.Context, db bun.IDB, workspaceID, contactID string) (*ContactDetail, error) {
	contact, err := loadContact(ctx, db, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	sourceChannel := SourceChannel{}
	if err := db.NewSelect().
		TableExpr("channels AS ch").
		ColumnExpr("ch.id::text AS id").
		Column("type", "name").
		Where("ch.workspace_id = ?", workspaceID).
		Where("ch.id = ?", contact.SourceChannelID).
		Scan(ctx, &sourceChannel); err != nil {
		return nil, fmt.Errorf("read contact source channel: %w", err)
	}

	methods := make([]ContactMethod, 0)
	if err := db.NewSelect().
		TableExpr("contact_methods AS cm").
		Column("type", "value", "label", "is_primary").
		Where("cm.workspace_id = ?", workspaceID).
		Where("cm.contact_id = ?", contactID).
		OrderExpr("cm.type ASC, cm.is_primary DESC, cm.created_at ASC").
		Scan(ctx, &methods); err != nil {
		return nil, fmt.Errorf("list contact methods: %w", err)
	}

	identities := make([]ChannelIdentity, 0)
	if err := db.NewSelect().
		TableExpr("channel_identities AS ci").
		ColumnExpr("ci.channel_id::text AS channel_id").
		ColumnExpr("ch.name AS channel_name").
		ColumnExpr("ci.external_id").
		ColumnExpr("ci.display_name").
		Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Where("ci.workspace_id = ?", workspaceID).
		Where("ci.contact_id = ?", contactID).
		OrderExpr("ci.updated_at DESC, ci.id DESC").
		Scan(ctx, &identities); err != nil {
		return nil, fmt.Errorf("list contact channel identities: %w", err)
	}

	var avatarFileID, name *string
	if err := db.NewSelect().
		TableExpr("contacts AS c").
		ColumnExpr(contactAvatarFileIDColumn).
		ColumnExpr(contactname.Expr("c", contactname.LatestIdentityName("c"))+" AS name").
		Where("c.workspace_id = ?", workspaceID).
		Where("c.id = ?", contactID).
		Scan(ctx, &avatarFileID, &name); err != nil {
		return nil, fmt.Errorf("read contact avatar and name: %w", err)
	}

	profile, err := contactprofile.Load(ctx, db, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	return &ContactDetail{Contact: *contact, Name: name, AvatarFileID: avatarFileID, SourceChannel: sourceChannel, Methods: methods, ChannelIdentities: identities, Profile: profile}, nil
}
