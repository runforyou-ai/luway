//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/runforyou-ai/support/validate"
	"github.com/uptrace/bun"
)

// ExtractionField 是设置了 AI 填写说明的联系人字段及联系人当前取值；单选取值为选项名称，Writable 表示 AI 可以写入。
type ExtractionField struct {
	ID            string
	Name          string
	Type          domain.ContactFieldType
	Options       []string
	AIInstruction string
	Value         string
	Writable      bool
}

// ExtractionTag 是设置了 AI 添加条件且联系人尚未拥有的标签。
type ExtractionTag struct {
	ID            string `bun:"id"`
	Name          string `bun:"name"`
	AIInstruction string `bun:"ai_instruction"`
}

// ExtractionContext 是 AI 从对话中抽取联系人资料所需的现有档案。
type ExtractionContext struct {
	Fields []ExtractionField
	Tags   []ExtractionTag
	Emails []string
	Phones []string
}

// Extraction 是 AI 从一次客服周期中抽取的联系人资料；Fields 以字段编号为键，单选取值为选项名称。
type Extraction struct {
	Fields map[string]string
	Emails []string
	Phones []string
	TagIDs []string
}

// LoadExtractionContext 读取未删除联系人的 AI 可填写的字段与当前取值、AI 可添加的标签和现有联系方式；联系人不存在时返回 ErrContactNotFound。
func LoadExtractionContext(ctx context.Context, db bun.IDB, workspaceID, contactID string) (ExtractionContext, error) {
	exists, err := db.NewSelect().TableExpr("contacts AS c").
		Where("c.workspace_id = ? AND c.id = ? AND c.deleted_at IS NULL", workspaceID, contactID).
		Exists(ctx)
	if err != nil {
		return ExtractionContext{}, fmt.Errorf("load extraction contact: %w", err)
	}
	if !exists {
		return ExtractionContext{}, ErrContactNotFound
	}
	result := ExtractionContext{Fields: make([]ExtractionField, 0), Tags: make([]ExtractionTag, 0)}
	fields := make([]struct {
		ID            string                      `bun:"id"`
		Name          string                      `bun:"name"`
		Type          domain.ContactFieldType     `bun:"type"`
		Options       []domain.ContactFieldOption `bun:"options,type:jsonb"`
		AIInstruction string                      `bun:"ai_instruction"`
		Value         sql.NullString              `bun:"value"`
		Source        sql.NullString              `bun:"source"`
	}, 0)
	if err := db.NewSelect().TableExpr("contact_fields AS cf").
		ColumnExpr("cf.id::text AS id, cf.name, cf.type, cf.options, cf.ai_instruction, cfv.value, cfv.source").
		Join("LEFT JOIN contact_field_values AS cfv ON cfv.field_id = cf.id AND cfv.workspace_id = cf.workspace_id AND cfv.contact_id = ?", contactID).
		Where("cf.workspace_id = ? AND cf.ai_instruction <> ''", workspaceID).
		OrderExpr("cf.created_at ASC, cf.id ASC").
		Scan(ctx, &fields); err != nil {
		return ExtractionContext{}, fmt.Errorf("load extraction fields: %w", err)
	}
	for _, field := range fields {
		item := ExtractionField{
			ID: field.ID, Name: field.Name, Type: field.Type, AIInstruction: field.AIInstruction, Value: field.Value.String,
			Writable: !field.Source.Valid || domain.ContactProfileSource(field.Source.String) == domain.ContactProfileSourceAI,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, option.Name)
			// 单选取值换成选项名称。
			if option.ID == field.Value.String {
				item.Value = option.Name
			}
		}
		result.Fields = append(result.Fields, item)
	}
	if err := db.NewSelect().TableExpr("contact_tags AS ctg").
		ColumnExpr("ctg.id::text AS id, ctg.name, ctg.ai_instruction").
		Where("ctg.workspace_id = ? AND ctg.ai_instruction <> ''", workspaceID).
		Where("NOT EXISTS (SELECT 1 FROM contact_tag_assignments AS cta WHERE cta.workspace_id = ctg.workspace_id AND cta.contact_id = ? AND cta.tag_id = ctg.id)", contactID).
		OrderExpr("lower(ctg.name) ASC, ctg.id ASC").
		Scan(ctx, &result.Tags); err != nil {
		return ExtractionContext{}, fmt.Errorf("load extraction tags: %w", err)
	}
	methods := make([]servermodels.ContactMethod, 0)
	if err := db.NewSelect().Model(&methods).
		Where("cm.workspace_id = ? AND cm.contact_id = ?", workspaceID, contactID).
		OrderExpr("cm.created_at ASC, cm.id ASC").
		Scan(ctx); err != nil {
		return ExtractionContext{}, fmt.Errorf("load extraction contact methods: %w", err)
	}
	for _, method := range methods {
		switch domain.ContactMethodType(method.Type) {
		case domain.ContactMethodTypeEmail:
			result.Emails = append(result.Emails, method.Value)
		case domain.ContactMethodTypePhone:
			result.Phones = append(result.Phones, method.Value)
		}
	}
	return result, nil
}

// ApplyExtraction 在调用方事务中按来源优先级写入 closedAt 时关闭的客服周期的 AI 抽取结果并返回档案是否变化；字段只覆盖空值或由 AI 依据更早关闭的周期写入的值，联系方式达到上限时停止追加，联系人已删除时返回 ErrContactNotFound。
func ApplyExtraction(ctx context.Context, tx bun.Tx, workspaceID, contactID, serviceSessionID string, closedAt time.Time, extraction Extraction) (bool, error) {
	if err := lockContact(ctx, tx, workspaceID, contactID); err != nil {
		return false, err
	}
	changedAny := false
	for fieldID, raw := range extraction.Fields {
		ok, err := applyExtractedField(ctx, tx, workspaceID, contactID, serviceSessionID, closedAt, fieldID, raw)
		if err != nil {
			return false, err
		}
		changedAny = changedAny || ok
	}
	for _, tagID := range extraction.TagIDs {
		result, err := tx.NewRaw(`INSERT INTO contact_tag_assignments (workspace_id, contact_id, tag_id, source, source_service_session_id)
			SELECT ctg.workspace_id, ?, ctg.id, ?, ?
			FROM contact_tags AS ctg
			WHERE ctg.workspace_id = ? AND ctg.id = ? AND ctg.ai_instruction <> ''
			ON CONFLICT (contact_id, tag_id) DO NOTHING`,
			contactID, domain.ContactProfileSourceAI, serviceSessionID, workspaceID, tagID,
		).Exec(ctx)
		if err != nil {
			return false, fmt.Errorf("apply extracted contact tag: %w", err)
		}
		ok, err := changed(result)
		if err != nil {
			return false, err
		}
		changedAny = changedAny || ok
	}
	type extractedMethod struct {
		methodType domain.ContactMethodType
		value      string
	}
	methods := make([]extractedMethod, 0, len(extraction.Emails)+len(extraction.Phones))
	for _, value := range extraction.Emails {
		if normalized := strings.ToLower(strings.TrimSpace(value)); str.IsEmail(normalized) {
			methods = append(methods, extractedMethod{domain.ContactMethodTypeEmail, normalized})
		}
	}
	for _, value := range extraction.Phones {
		if normalized, ok := validate.E164(value); ok {
			methods = append(methods, extractedMethod{domain.ContactMethodTypePhone, normalized})
		}
	}
	for _, method := range methods {
		ok, err := AddMethod(ctx, tx, workspaceID, contactID, method.methodType, method.value)
		if err != nil {
			return false, fmt.Errorf("apply extracted contact method: %w", err)
		}
		changedAny = changedAny || ok
	}
	if !changedAny {
		return false, nil
	}
	return true, touchContact(ctx, tx, workspaceID, contactID)
}

// applyExtractedField 校验并写入一个 AI 抽取的字段取值，返回是否实际变化；字段已删除、AI 填写说明为空或取值不合法时跳过。
func applyExtractedField(ctx context.Context, tx bun.Tx, workspaceID, contactID, serviceSessionID string, closedAt time.Time, fieldID, raw string) (bool, error) {
	field := &servermodels.ContactField{}
	err := tx.NewSelect().Model(field).
		Where("cf.workspace_id = ? AND cf.id = ? AND cf.ai_instruction <> ''", workspaceID, fieldID).
		For("KEY SHARE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load extracted contact field: %w", err)
	}
	// 单选取值按选项名称忽略大小写换成选项编号。
	if domain.ContactFieldType(field.Type) == domain.ContactFieldTypeSelect {
		index := slices.IndexFunc(field.Options, func(option domain.ContactFieldOption) bool {
			return strings.EqualFold(option.Name, strings.TrimSpace(raw))
		})
		if index < 0 {
			return false, nil
		}
		raw = field.Options[index].ID
	}
	value, code := normalizeValue(field, raw)
	if code != "" || value == "" {
		return false, nil
	}
	result, err := tx.NewInsert().Model(&servermodels.ContactFieldValue{
		WorkspaceID: workspaceID, ContactID: contactID, FieldID: fieldID, Value: value,
		Source: string(domain.ContactProfileSourceAI), SourceServiceSessionID: &serviceSessionID, SourceSessionClosedAt: &closedAt,
	}).
		Column("workspace_id", "contact_id", "field_id", "value", "source", "source_service_session_id", "source_session_closed_at").
		On("CONFLICT (contact_id, field_id) DO UPDATE").
		Set("value = EXCLUDED.value, source_service_session_id = EXCLUDED.source_service_session_id, source_session_closed_at = EXCLUDED.source_session_closed_at").
		// 现有取值依据的周期关闭时间晚于本次时保留现有取值。
		Where("cfv.source = ? AND cfv.value <> EXCLUDED.value AND cfv.source_session_closed_at <= EXCLUDED.source_session_closed_at", domain.ContactProfileSourceAI).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("apply extracted contact field: %w", err)
	}
	return changed(result)
}
