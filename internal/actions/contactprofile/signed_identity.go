//go:build server

package contactprofile

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ApplySignedProfile 在调用方事务中写入客户签名身份带入的档案：先对联系人取 FOR UPDATE，与客服编辑、AI 抽取和其他签名身份同步串行；字段与标签按名称忽略大小写匹配，单选取值按选项名称匹配，未定义的字段、不合法的取值和不存在的标签跳过；取值覆盖任何来源，取值为空时只删除签名身份写入的取值；提供标签时补齐缺少的标签并接管其来源，移除签名身份添加而本次未给出的标签；档案实际变化时更新联系人并通知客户端。
func ApplySignedProfile(ctx context.Context, db bun.IDB, organizationID, contactID string, profile domain.SignedContactProfile) error {
	if len(profile.Attributes) == 0 && profile.Tags == nil {
		return nil
	}
	if _, err := db.NewSelect().TableExpr("contacts AS c").Column("c.id").
		Where("c.organization_id = ? AND c.id = ?", organizationID, contactID).
		For("UPDATE").
		Exec(ctx); err != nil {
		return fmt.Errorf("lock signed profile contact: %w", err)
	}
	changedAny := false
	if len(profile.Attributes) > 0 {
		names := make([]string, 0, len(profile.Attributes))
		values := make(map[string]string, len(profile.Attributes))
		for name, value := range profile.Attributes {
			names = append(names, strings.ToLower(name))
			values[strings.ToLower(name)] = value
		}
		fields := make([]*servermodels.ContactField, 0, len(names))
		if err := db.NewSelect().Model(&fields).
			Where("cf.organization_id = ? AND lower(cf.name) IN (?)", organizationID, bun.In(names)).
			For("KEY SHARE").
			Scan(ctx); err != nil {
			return fmt.Errorf("load signed contact fields: %w", err)
		}
		for _, field := range fields {
			ok, err := applySignedField(ctx, db, organizationID, contactID, field, values[strings.ToLower(field.Name)])
			if err != nil {
				return err
			}
			changedAny = changedAny || ok
		}
	}
	if profile.Tags != nil {
		ok, err := applySignedTags(ctx, db, organizationID, contactID, profile.Tags)
		if err != nil {
			return err
		}
		changedAny = changedAny || ok
	}
	if !changedAny {
		return nil
	}
	return touchContact(ctx, db, organizationID, contactID)
}

// applySignedField 写入或清除一个签名身份带入的字段取值并返回是否实际变化，取值不合法时跳过。
func applySignedField(ctx context.Context, db bun.IDB, organizationID, contactID string, field *servermodels.ContactField, raw string) (bool, error) {
	if raw == "" {
		result, err := db.NewDelete().Model((*servermodels.ContactFieldValue)(nil)).
			Where("organization_id = ? AND contact_id = ? AND field_id = ? AND source = ?", organizationID, contactID, field.ID, domain.ContactProfileSourceSignedIdentity).
			Exec(ctx)
		if err != nil {
			return false, fmt.Errorf("clear signed contact field: %w", err)
		}
		return changed(result)
	}
	// 单选取值按选项名称忽略大小写换成选项编号。
	if domain.ContactFieldType(field.Type) == domain.ContactFieldTypeSelect {
		index := slices.IndexFunc(field.Options, func(option domain.ContactFieldOption) bool { return strings.EqualFold(option.Name, raw) })
		if index < 0 {
			return false, nil
		}
		raw = field.Options[index].ID
	}
	value, code := normalizeValue(field, raw)
	if code != "" || value == "" {
		return false, nil
	}
	result, err := db.NewInsert().Model(&servermodels.ContactFieldValue{
		OrganizationID: organizationID, ContactID: contactID, FieldID: field.ID, Value: value,
		Source: string(domain.ContactProfileSourceSignedIdentity),
	}).
		Column("organization_id", "contact_id", "field_id", "value", "source").
		On("CONFLICT (contact_id, field_id) DO UPDATE").
		Set("value = EXCLUDED.value, source = EXCLUDED.source, source_user_id = NULL, source_service_session_id = NULL, source_session_closed_at = NULL, updated_at = now()").
		Where("(cfv.value, cfv.source) IS DISTINCT FROM (EXCLUDED.value, EXCLUDED.source)").
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("apply signed contact field: %w", err)
	}
	return changed(result)
}

// applySignedTags 让联系人上来源为签名身份的标签与给出的标签名称一致并返回是否实际变化，不存在的标签名称跳过。
func applySignedTags(ctx context.Context, db bun.IDB, organizationID, contactID string, names []string) (bool, error) {
	tagIDs := make([]string, 0, len(names))
	if len(names) > 0 {
		lowered := make([]string, 0, len(names))
		for _, name := range names {
			lowered = append(lowered, strings.ToLower(name))
		}
		if err := db.NewSelect().TableExpr("contact_tags AS ctg").Column("ctg.id").
			Where("ctg.organization_id = ? AND lower(ctg.name) IN (?)", organizationID, bun.In(lowered)).
			For("KEY SHARE").
			Scan(ctx, &tagIDs); err != nil {
			return false, fmt.Errorf("load signed contact tags: %w", err)
		}
	}
	changedAny := false
	for _, tagID := range tagIDs {
		result, err := db.NewInsert().Model(&servermodels.ContactTagAssignment{
			OrganizationID: organizationID, ContactID: contactID, TagID: tagID,
			Source: string(domain.ContactProfileSourceSignedIdentity),
		}).
			Column("organization_id", "contact_id", "tag_id", "source").
			On("CONFLICT (contact_id, tag_id) DO UPDATE").
			Set("source = EXCLUDED.source, source_user_id = NULL, source_service_session_id = NULL, updated_at = now()").
			Where("cta.source <> EXCLUDED.source").
			Exec(ctx)
		if err != nil {
			return false, fmt.Errorf("apply signed contact tag: %w", err)
		}
		ok, err := changed(result)
		if err != nil {
			return false, err
		}
		changedAny = changedAny || ok
	}
	query := db.NewDelete().Model((*servermodels.ContactTagAssignment)(nil)).
		Where("organization_id = ? AND contact_id = ? AND source = ?", organizationID, contactID, domain.ContactProfileSourceSignedIdentity)
	if len(tagIDs) > 0 {
		query = query.Where("tag_id NOT IN (?)", bun.In(tagIDs))
	}
	result, err := query.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("remove signed contact tags: %w", err)
	}
	ok, err := changed(result)
	if err != nil {
		return false, err
	}
	return changedAny || ok, nil
}
