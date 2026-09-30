//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// SetFieldValueAction 由客服填写或清空联系人字段。
type SetFieldValueAction struct{ db *bun.DB }

// NewSetFieldValueAction 创建联系人字段取值编辑操作。
func NewSetFieldValueAction(db *bun.DB) *SetFieldValueAction { return &SetFieldValueAction{db: db} }

// Execute 按字段类型校验并保存取值，来源记为客服并覆盖 AI 写入的取值；取值为空时删除该字段的取值；网站同步的取值返回 ErrSyncedFromWebsite；取值实际变化时更新联系人并通知客户端。
func (a *SetFieldValueAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID, fieldID, value string) error {
	if !common.ValidUUID(fieldID) {
		return ErrFieldNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockContact(ctx, tx, identity.Organization.ID, contactID); err != nil {
			return err
		}
		field := &servermodels.ContactField{}
		err := tx.NewSelect().Model(field).
			Where("cf.organization_id = ? AND cf.id = ?", identity.Organization.ID, fieldID).
			For("KEY SHARE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFieldNotFound
		}
		if err != nil {
			return err
		}
		normalized, code := normalizeValue(field, value)
		if code != "" {
			return &common.FieldError{Fields: map[string]common.FieldCode{"value": code}}
		}
		// 网站同步的取值只由网站维护，写入条件排除网站来源。
		var result sql.Result
		if normalized == "" {
			result, err = tx.NewDelete().Model((*servermodels.ContactFieldValue)(nil)).
				Where("organization_id = ? AND contact_id = ? AND field_id = ? AND source <> ?", identity.Organization.ID, contactID, fieldID, domain.ContactProfileSourceWebsite).
				Exec(ctx)
		} else {
			userID := identity.User.ID
			result, err = tx.NewInsert().Model(&servermodels.ContactFieldValue{
				OrganizationID: identity.Organization.ID, ContactID: contactID, FieldID: fieldID, Value: normalized,
				Source: string(domain.ContactProfileSourceMember), SourceUserID: &userID,
			}).
				Column("organization_id", "contact_id", "field_id", "value", "source", "source_user_id").
				On("CONFLICT (contact_id, field_id) DO UPDATE").
				Set("value = EXCLUDED.value, source = EXCLUDED.source, source_user_id = EXCLUDED.source_user_id, source_service_session_id = NULL, source_session_closed_at = NULL, updated_at = now()").
				Where("cfv.source <> ? AND (cfv.value, cfv.source, cfv.source_user_id) IS DISTINCT FROM (EXCLUDED.value, EXCLUDED.source, EXCLUDED.source_user_id)", domain.ContactProfileSourceWebsite).
				Exec(ctx)
		}
		if err != nil {
			return err
		}
		ok, err := changed(result)
		if err != nil {
			return err
		}
		if !ok {
			return syncedFromWebsite(ctx, tx.NewSelect().Model((*servermodels.ContactFieldValue)(nil)).
				Where("cfv.organization_id = ? AND cfv.contact_id = ? AND cfv.field_id = ?", identity.Organization.ID, contactID, fieldID))
		}
		return touchContact(ctx, tx, identity.Organization.ID, contactID)
	})
	if err != nil {
		return fmt.Errorf("set contact field value: %w", err)
	}
	return nil
}

// AddTagAction 由客服给联系人添加标签。
type AddTagAction struct{ db *bun.DB }

// NewAddTagAction 创建联系人标签添加操作。
func NewAddTagAction(db *bun.DB) *AddTagAction { return &AddTagAction{db: db} }

// Execute 给联系人添加标签，来源记为客服；联系人已有该标签时保持原记录，新增时更新联系人并通知客户端。
func (a *AddTagAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID, tagID string) error {
	if !common.ValidUUID(tagID) {
		return ErrTagNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockContact(ctx, tx, identity.Organization.ID, contactID); err != nil {
			return err
		}
		var id string
		err := tx.NewSelect().TableExpr("contact_tags AS ctg").Column("ctg.id").
			Where("ctg.organization_id = ? AND ctg.id = ?", identity.Organization.ID, tagID).
			For("KEY SHARE").
			Scan(ctx, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTagNotFound
		}
		if err != nil {
			return err
		}
		userID := identity.User.ID
		result, err := tx.NewInsert().Model(&servermodels.ContactTagAssignment{
			OrganizationID: identity.Organization.ID, ContactID: contactID, TagID: tagID,
			Source: string(domain.ContactProfileSourceMember), SourceUserID: &userID,
		}).
			Column("organization_id", "contact_id", "tag_id", "source", "source_user_id").
			On("CONFLICT (contact_id, tag_id) DO NOTHING").
			Exec(ctx)
		if err != nil {
			return err
		}
		if ok, err := changed(result); err != nil || !ok {
			return err
		}
		return touchContact(ctx, tx, identity.Organization.ID, contactID)
	})
	if err != nil {
		return fmt.Errorf("add contact tag: %w", err)
	}
	return nil
}

// RemoveTagAction 由客服移除联系人上的标签。
type RemoveTagAction struct{ db *bun.DB }

// NewRemoveTagAction 创建联系人标签移除操作。
func NewRemoveTagAction(db *bun.DB) *RemoveTagAction { return &RemoveTagAction{db: db} }

// Execute 移除联系人上的标签并更新联系人、通知客户端；联系人没有该标签时不做改动，网站同步的标签返回 ErrSyncedFromWebsite。
func (a *RemoveTagAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID, tagID string) error {
	if !common.ValidUUID(tagID) {
		return ErrTagNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockContact(ctx, tx, identity.Organization.ID, contactID); err != nil {
			return err
		}
		// 网站同步的标签只由网站维护，删除条件排除网站来源。
		result, err := tx.NewDelete().Model((*servermodels.ContactTagAssignment)(nil)).
			Where("organization_id = ? AND contact_id = ? AND tag_id = ? AND source <> ?", identity.Organization.ID, contactID, tagID, domain.ContactProfileSourceWebsite).
			Exec(ctx)
		if err != nil {
			return err
		}
		ok, err := changed(result)
		if err != nil {
			return err
		}
		if !ok {
			return syncedFromWebsite(ctx, tx.NewSelect().Model((*servermodels.ContactTagAssignment)(nil)).
				Where("cta.organization_id = ? AND cta.contact_id = ? AND cta.tag_id = ?", identity.Organization.ID, contactID, tagID))
		}
		return touchContact(ctx, tx, identity.Organization.ID, contactID)
	})
	if err != nil {
		return fmt.Errorf("remove contact tag: %w", err)
	}
	return nil
}

// sourceSessionColumn 读取 AI 写入所依据的客服周期在会话中的位置，来源周期表别名为 ss。
const sourceSessionColumn = "CASE WHEN ss.id IS NULL THEN NULL ELSE jsonb_build_object('conversationId', ss.conversation_id, 'openingMessageId', ss.opening_message_id) END AS source_session"

// Load 读取联系人档案：字段取值按字段创建顺序排列，标签按名称排列；AI 写入的项附带来源周期。
func Load(ctx context.Context, db bun.IDB, organizationID, contactID string) (Profile, error) {
	profile := Profile{Fields: make([]FieldValue, 0), Tags: make([]AssignedTag, 0)}
	if err := db.NewSelect().TableExpr("contact_field_values AS cfv").
		ColumnExpr("cfv.field_id::text AS field_id, cfv.value, cfv.source, cfv.updated_at").
		ColumnExpr(sourceSessionColumn).
		Join("JOIN contact_fields AS cf ON cf.id = cfv.field_id AND cf.organization_id = cfv.organization_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = cfv.source_service_session_id AND ss.organization_id = cfv.organization_id").
		Where("cfv.organization_id = ? AND cfv.contact_id = ?", organizationID, contactID).
		OrderExpr("cf.created_at ASC, cf.id ASC").
		Scan(ctx, &profile.Fields); err != nil {
		return Profile{}, fmt.Errorf("load contact field values: %w", err)
	}
	if err := db.NewSelect().TableExpr("contact_tag_assignments AS cta").
		ColumnExpr("ctg.id::text AS id, ctg.name, cta.source").
		ColumnExpr(sourceSessionColumn).
		Join("JOIN contact_tags AS ctg ON ctg.id = cta.tag_id AND ctg.organization_id = cta.organization_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = cta.source_service_session_id AND ss.organization_id = cta.organization_id").
		Where("cta.organization_id = ? AND cta.contact_id = ?", organizationID, contactID).
		OrderExpr("lower(ctg.name) ASC, ctg.id ASC").
		Scan(ctx, &profile.Tags); err != nil {
		return Profile{}, fmt.Errorf("load contact tags: %w", err)
	}
	return profile, nil
}

// LoadAgentProfile 读取联系人的阶段、标签、字段取值与备注并转换为提供给 AI 的档案；字段以显示名称为键，单选取值换成选项名称。
func LoadAgentProfile(ctx context.Context, db bun.IDB, organizationID, contactID string) (agentruntime.CustomerProfile, error) {
	contact := struct {
		Stage domain.ContactStage `bun:"stage"`
		Notes *string             `bun:"notes"`
	}{}
	if err := db.NewSelect().TableExpr("contacts AS c").Column("c.stage", "c.notes").
		Where("c.organization_id = ? AND c.id = ?", organizationID, contactID).
		Scan(ctx, &contact); err != nil {
		return agentruntime.CustomerProfile{}, fmt.Errorf("load agent contact profile: %w", err)
	}
	profile := agentruntime.CustomerProfile{Stage: string(contact.Stage)}
	if contact.Notes != nil {
		profile.Notes = *contact.Notes
	}
	values := make([]struct {
		Name    string                      `bun:"name"`
		Type    domain.ContactFieldType     `bun:"type"`
		Options []domain.ContactFieldOption `bun:"options,type:jsonb"`
		Value   string                      `bun:"value"`
	}, 0)
	if err := db.NewSelect().TableExpr("contact_field_values AS cfv").
		ColumnExpr("cf.name, cf.type, cf.options, cfv.value").
		Join("JOIN contact_fields AS cf ON cf.id = cfv.field_id AND cf.organization_id = cfv.organization_id").
		Where("cfv.organization_id = ? AND cfv.contact_id = ?", organizationID, contactID).
		Scan(ctx, &values); err != nil {
		return agentruntime.CustomerProfile{}, fmt.Errorf("load agent contact field values: %w", err)
	}
	if len(values) > 0 {
		profile.Fields = make(map[string]string, len(values))
	}
	for _, value := range values {
		text := value.Value
		// 单选取值换成选项名称。
		if value.Type == domain.ContactFieldTypeSelect {
			index := slices.IndexFunc(value.Options, func(option domain.ContactFieldOption) bool { return option.ID == value.Value })
			if index < 0 {
				continue
			}
			text = value.Options[index].Name
		}
		profile.Fields[value.Name] = text
	}
	if err := db.NewSelect().TableExpr("contact_tag_assignments AS cta").
		ColumnExpr("ctg.name").
		Join("JOIN contact_tags AS ctg ON ctg.id = cta.tag_id AND ctg.organization_id = cta.organization_id").
		Where("cta.organization_id = ? AND cta.contact_id = ?", organizationID, contactID).
		OrderExpr("lower(ctg.name) ASC, ctg.id ASC").
		Scan(ctx, &profile.Tags); err != nil {
		return agentruntime.CustomerProfile{}, fmt.Errorf("load agent contact tags: %w", err)
	}
	return profile, nil
}

// decimalPattern 匹配可带符号和小数部分的十进制数字文本。
var decimalPattern = regexp.MustCompile(`^([+-]?)(\d+)(?:\.(\d+))?$`)

// normalizeValue 按字段类型规范化取值：数字按十进制文本规范化，日期校验为 YYYY-MM-DD，单选校验选项编号；空值表示清空。
func normalizeValue(field *servermodels.ContactField, value string) (string, common.FieldCode) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	switch domain.ContactFieldType(field.Type) {
	case domain.ContactFieldTypeNumber:
		match := decimalPattern.FindStringSubmatch(value)
		if match == nil {
			return "", ValidationValueInvalid
		}
		// 按十进制文本去掉正号、整数前导零和小数末尾零，负零写作 0。
		integer := strings.TrimLeft(match[2], "0")
		if integer == "" {
			integer = "0"
		}
		normalized := integer
		if fraction := strings.TrimRight(match[3], "0"); fraction != "" {
			normalized += "." + fraction
		}
		if match[1] == "-" && normalized != "0" {
			normalized = "-" + normalized
		}
		if utf8.RuneCountInString(normalized) > domain.ContactFieldValueMaxLength {
			return "", ValidationValueTooLong
		}
		return normalized, ""
	case domain.ContactFieldTypeDate:
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			return "", ValidationValueInvalid
		}
		return value, ""
	case domain.ContactFieldTypeSelect:
		if !slices.ContainsFunc(field.Options, func(option domain.ContactFieldOption) bool { return option.ID == value }) {
			return "", ValidationValueInvalid
		}
		return value, ""
	}
	if utf8.RuneCountInString(value) > domain.ContactFieldValueMaxLength {
		return "", ValidationValueTooLong
	}
	return value, ""
}
