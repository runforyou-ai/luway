//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// ListFieldsQuery 读取企业联系人字段。
type ListFieldsQuery struct{ db *bun.DB }

// NewListFieldsQuery 创建联系人字段列表查询。
func NewListFieldsQuery(db *bun.DB) *ListFieldsQuery { return &ListFieldsQuery{db: db} }

// Execute 按创建顺序返回当前企业的联系人字段。
func (q *ListFieldsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Field, error) {
	fields, err := listFields(ctx, q.db, identity.Organization.ID)
	if err != nil {
		return nil, fmt.Errorf("list contact fields: %w", err)
	}
	return fields, nil
}

// CreateFieldAction 新增企业联系人字段。
type CreateFieldAction struct{ db *bun.DB }

// NewCreateFieldAction 创建联系人字段新增操作。
func NewCreateFieldAction(db *bun.DB) *CreateFieldAction { return &CreateFieldAction{db: db} }

// Execute 校验并新增当前企业的联系人字段，由服务端为全部选项分配编号。
func (a *CreateFieldAction) Execute(ctx context.Context, identity *servermodels.Identity, input FieldInput) (*Field, error) {
	input, codes := normalizeFieldInput(input)
	if len(codes) > 0 {
		return nil, &common.FieldError{Fields: codes}
	}
	var field *Field
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 新字段的选项编号全部由服务端分配。
		for index := range input.Options {
			input.Options[index].ID = ""
		}
		model := &servermodels.ContactField{
			OrganizationID: identity.Organization.ID, Name: input.Name, Type: string(input.Type),
			Options: assignOptionIDs(input.Options), AIInstruction: input.AIInstruction,
		}
		_, err := tx.NewInsert().Model(model).
			Column("organization_id", "name", "type", "options", "ai_instruction").
			Returning("id").
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		field, err = loadField(ctx, tx, identity.Organization.ID, model.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create contact field: %w", err)
	}
	return field, nil
}

// UpdateFieldAction 修改企业联系人字段。
type UpdateFieldAction struct{ db *bun.DB }

// NewUpdateFieldAction 创建联系人字段修改操作。
func NewUpdateFieldAction(db *bun.DB) *UpdateFieldAction { return &UpdateFieldAction{db: db} }

// Execute 修改字段名称、单选选项与 AI 填写说明并通知有取值的联系人所在客户端重读档案；字段类型不可修改，被移除的选项对应的取值在同一事务中清空。
func (a *UpdateFieldAction) Execute(ctx context.Context, identity *servermodels.Identity, fieldID string, input FieldInput) (*Field, error) {
	if !common.ValidUUID(fieldID) {
		return nil, ErrFieldNotFound
	}
	input, codes := normalizeFieldInput(input)
	if len(codes) > 0 {
		return nil, &common.FieldError{Fields: codes}
	}
	var field *Field
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored := &servermodels.ContactField{}
		err := tx.NewSelect().Model(stored).
			Where("cf.organization_id = ? AND cf.id = ?", identity.Organization.ID, fieldID).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFieldNotFound
		}
		if err != nil {
			return err
		}
		if stored.Type != string(input.Type) {
			return &common.FieldError{Fields: map[string]common.FieldCode{"type": ValidationFieldTypeImmutable}}
		}
		// 选项编号只接受该字段已有的编号，且同一编号只出现一次。
		seen := make(map[string]struct{}, len(input.Options))
		for _, option := range input.Options {
			if option.ID == "" {
				continue
			}
			_, duplicate := seen[option.ID]
			if duplicate || !slices.ContainsFunc(stored.Options, func(existing domain.ContactFieldOption) bool { return existing.ID == option.ID }) {
				return &common.FieldError{Fields: map[string]common.FieldCode{"options": ValidationOptionInvalid}}
			}
			seen[option.ID] = struct{}{}
		}
		options := assignOptionIDs(input.Options)
		_, err = tx.NewUpdate().Model((*servermodels.ContactField)(nil)).
			Set("name = ?", input.Name).
			Set("options = ?", options).
			Set("ai_instruction = ?", input.AIInstruction).
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, fieldID).
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		// 名称与选项变化都会改变提供给客户端和 AI 的档案，清空取值前通知相关联系人。
		if err := touchFieldContacts(ctx, tx, identity.Organization.ID, fieldID); err != nil {
			return err
		}
		if input.Type == domain.ContactFieldTypeSelect {
			kept := make([]string, 0, len(options))
			for _, option := range options {
				kept = append(kept, option.ID)
			}
			if _, err := tx.NewDelete().Model((*servermodels.ContactFieldValue)(nil)).
				Where("organization_id = ? AND field_id = ?", identity.Organization.ID, fieldID).
				Where("value NOT IN (?)", bun.In(kept)).
				Exec(ctx); err != nil {
				return err
			}
		}
		field, err = loadField(ctx, tx, identity.Organization.ID, fieldID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update contact field: %w", err)
	}
	return field, nil
}

// DeleteFieldAction 删除企业联系人字段。
type DeleteFieldAction struct{ db *bun.DB }

// NewDeleteFieldAction 创建联系人字段删除操作。
func NewDeleteFieldAction(db *bun.DB) *DeleteFieldAction { return &DeleteFieldAction{db: db} }

// Execute 删除当前企业的联系人字段及所有联系人在该字段上的取值，并通知这些联系人所在客户端重读档案。
func (a *DeleteFieldAction) Execute(ctx context.Context, identity *servermodels.Identity, fieldID string) error {
	if !common.ValidUUID(fieldID) {
		return ErrFieldNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewDelete().Model((*servermodels.ContactField)(nil)).
			Where("organization_id = ? AND id = ?", identity.Organization.ID, fieldID).
			Exec(ctx)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrFieldNotFound
		}
		if err := touchFieldContacts(ctx, tx, identity.Organization.ID, fieldID); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*servermodels.ContactFieldValue)(nil)).
			Where("organization_id = ? AND field_id = ?", identity.Organization.ID, fieldID).
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete contact field: %w", err)
	}
	return nil
}

// normalizeFieldInput 规范化并校验字段名称、类型、选项与 AI 填写说明；非单选字段忽略选项。
func normalizeFieldInput(input FieldInput) (FieldInput, map[string]common.FieldCode) {
	codes := make(map[string]common.FieldCode)
	var code common.FieldCode
	if input.Name, code = normalizeName(input.Name, domain.ContactFieldNameMaxLength); code != "" {
		codes["name"] = code
	}
	if input.AIInstruction, code = normalizeAIInstruction(input.AIInstruction); code != "" {
		codes["aiInstruction"] = code
	}
	switch input.Type {
	case domain.ContactFieldTypeText, domain.ContactFieldTypeNumber, domain.ContactFieldTypeDate:
		input.Options = nil
	case domain.ContactFieldTypeSelect:
		names := make(map[string]struct{}, len(input.Options))
		for index, option := range input.Options {
			name, code := normalizeName(option.Name, domain.ContactFieldOptionNameMaxLength)
			if code != "" {
				codes["options"] = ValidationOptionInvalid
				break
			}
			key := strings.ToLower(name)
			if _, duplicate := names[key]; duplicate {
				codes["options"] = ValidationOptionDuplicate
				break
			}
			names[key] = struct{}{}
			input.Options[index].Name = name
		}
		if len(input.Options) == 0 {
			codes["options"] = ValidationOptionsRequired
		}
	default:
		codes["type"] = ValidationFieldTypeInvalid
	}
	return input, codes
}

// assignOptionIDs 为没有编号的新选项分配编号，保持选项顺序。
func assignOptionIDs(options []domain.ContactFieldOption) []domain.ContactFieldOption {
	assigned := make([]domain.ContactFieldOption, 0, len(options))
	for _, option := range options {
		if option.ID == "" {
			option.ID = uuid.NewString()
		}
		assigned = append(assigned, option)
	}
	return assigned
}

// selectFields 构造当前企业联系人字段的查询。
func selectFields(db bun.IDB, organizationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("contact_fields AS cf").
		ColumnExpr("cf.id::text AS id, cf.name, cf.type, cf.options, cf.ai_instruction, cf.created_at, cf.updated_at").
		Where("cf.organization_id = ?", organizationID)
}

// listFields 按创建顺序读取当前企业的联系人字段。
func listFields(ctx context.Context, db bun.IDB, organizationID string) ([]Field, error) {
	fields := make([]Field, 0)
	err := selectFields(db, organizationID).OrderExpr("cf.created_at ASC, cf.id ASC").Scan(ctx, &fields)
	return fields, err
}

// loadField 读取当前企业的联系人字段。
func loadField(ctx context.Context, db bun.IDB, organizationID, fieldID string) (*Field, error) {
	field := &Field{}
	err := selectFields(db, organizationID).Where("cf.id = ?", fieldID).Scan(ctx, field)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFieldNotFound
	}
	return field, err
}
