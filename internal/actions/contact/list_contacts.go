//go:build server

package contact

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListContactsQuery 读取当前企业的外部联系人列表。
type ListContactsQuery struct {
	db *bun.DB
}

// NewListContactsQuery 创建联系人列表查询。
func NewListContactsQuery(db *bun.DB) *ListContactsQuery {
	return &ListContactsQuery{db: db}
}

// Execute 返回满足查询条件的分页联系人列表。
func (q *ListContactsQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListInput) (ListOutput, error) {
	input, fields := normalizeListInput(input)
	if len(fields) > 0 {
		return ListOutput{}, &ValidationError{Fields: fields}
	}

	countQuery := applyContactFilters(q.db.NewSelect().TableExpr("contacts AS c"), identity.Organization.ID, input)
	total, err := countQuery.Count(ctx)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count contacts: %w", err)
	}

	contacts := make([]ContactSummary, 0)
	query := applyContactFilters(q.db.NewSelect().TableExpr("contacts AS c"), identity.Organization.ID, input).
		ColumnExpr("c.id::text AS id").
		ColumnExpr("c.number").
		ColumnExpr(contactname.Expr("c", contactname.LatestIdentityName("c")) + " AS display_name").
		ColumnExpr(contactAvatarFileIDColumn).
		ColumnExpr("c.stage").
		ColumnExpr("c.created_at").
		ColumnExpr("c.deleted_at").
		ColumnExpr("source_channel.name AS source_channel_name").
		ColumnExpr(contactname.PrimaryEmail("c") + " AS primary_email").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = 'phone' ORDER BY cm.is_primary DESC, cm.created_at ASC LIMIT 1) AS primary_phone").
		Join("JOIN channels AS source_channel ON source_channel.id = c.source_channel_id AND source_channel.organization_id = c.organization_id")
	switch input.Sort {
	case domain.ContactSortCreatedAtDescending:
		query = query.OrderExpr("c.created_at DESC, c.id DESC")
	case domain.ContactSortDisplayNameAscending:
		query = query.OrderExpr("lower(coalesce(" + contactname.Expr("c", contactname.LatestIdentityName("c")) + ", '')) ASC, c.id ASC")
	default:
		query = query.OrderExpr("c.updated_at DESC, c.id DESC")
	}
	if err := query.
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &contacts); err != nil {
		return ListOutput{}, fmt.Errorf("list contacts: %w", err)
	}
	if err := attachTags(ctx, q.db, identity.Organization.ID, contacts); err != nil {
		return ListOutput{}, err
	}
	return ListOutput{
		Contacts: contacts,
		Page:     PageInfo{Number: input.Page, Size: input.PageSize, Total: total},
	}, nil
}

// applyContactFilters 添加组织边界和联系人筛选条件。
func applyContactFilters(query *bun.SelectQuery, organizationID string, input ListInput) *bun.SelectQuery {
	query = query.Where("c.organization_id = ?", organizationID)
	if input.Deleted {
		query = query.Where("c.deleted_at IS NOT NULL")
	} else {
		query = query.Where("c.deleted_at IS NULL")
	}
	if input.Stage != "" {
		query = query.Where("c.stage = ?", input.Stage)
	}
	if input.ChannelID != "" {
		query = query.Where("(c.source_channel_id = ? OR EXISTS (SELECT 1 FROM contact_channel_identities AS cci WHERE cci.organization_id = c.organization_id AND cci.contact_id = c.id AND cci.channel_id = ?))", input.ChannelID, input.ChannelID)
	}
	if input.TagID != "" {
		query = query.Where("EXISTS (SELECT 1 FROM contact_tag_assignments AS cta WHERE cta.organization_id = c.organization_id AND cta.contact_id = c.id AND cta.tag_id = ?)", input.TagID)
	}
	if input.MethodType != "" {
		query = query.Where("EXISTS (SELECT 1 FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = ?)", input.MethodType)
	}
	if input.Query != "" {
		pattern := common.ContainsPattern(input.Query)
		query = query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
			return group.
				Where("coalesce(c.display_name, '') ILIKE ?", pattern).
				WhereOr("EXISTS (SELECT 1 FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.normalized_value ILIKE ?)", pattern).
				WhereOr("EXISTS (SELECT 1 FROM contact_channel_identities AS cci WHERE cci.organization_id = c.organization_id AND cci.contact_id = c.id AND coalesce(cci.display_name, '') ILIKE ?)", pattern).
				WhereOr("c.number = ?", contactname.Number(input.Query))
		})
	}
	return query
}

// attachTags 为本页联系人按名称顺序填充标签。
func attachTags(ctx context.Context, db bun.IDB, organizationID string, contacts []ContactSummary) error {
	if len(contacts) == 0 {
		return nil
	}
	contactIDs := make([]string, 0, len(contacts))
	for index := range contacts {
		contactIDs = append(contactIDs, contacts[index].ID)
		contacts[index].Tags = make([]TagSummary, 0)
	}
	tags := make([]TagSummary, 0)
	if err := db.NewSelect().TableExpr("contact_tag_assignments AS cta").
		ColumnExpr("cta.contact_id::text AS contact_id, ctg.id::text AS id, ctg.name").
		Join("JOIN contact_tags AS ctg ON ctg.id = cta.tag_id AND ctg.organization_id = cta.organization_id").
		Where("cta.organization_id = ? AND cta.contact_id IN (?)", organizationID, bun.In(contactIDs)).
		OrderExpr("lower(ctg.name) ASC, ctg.id ASC").
		Scan(ctx, &tags); err != nil {
		return fmt.Errorf("list contact tags: %w", err)
	}
	indexByID := make(map[string]int, len(contacts))
	for index, contact := range contacts {
		indexByID[contact.ID] = index
	}
	for _, tag := range tags {
		index := indexByID[tag.ContactID]
		contacts[index].Tags = append(contacts[index].Tags, tag)
	}
	return nil
}
