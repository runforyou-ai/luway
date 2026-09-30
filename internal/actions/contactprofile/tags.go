//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// ListTagsQuery 读取企业联系人标签。
type ListTagsQuery struct{ db *bun.DB }

// NewListTagsQuery 创建联系人标签列表查询。
func NewListTagsQuery(db *bun.DB) *ListTagsQuery { return &ListTagsQuery{db: db} }

// Execute 按名称顺序返回当前企业的联系人标签。
func (q *ListTagsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Tag, error) {
	tags := make([]Tag, 0)
	if err := selectTags(q.db, identity.Organization.ID).OrderExpr("lower(ctg.name) ASC, ctg.id ASC").Scan(ctx, &tags); err != nil {
		return nil, fmt.Errorf("list contact tags: %w", err)
	}
	return tags, nil
}

// CreateTagAction 新增企业联系人标签。
type CreateTagAction struct{ db *bun.DB }

// NewCreateTagAction 创建联系人标签新增操作。
func NewCreateTagAction(db *bun.DB) *CreateTagAction { return &CreateTagAction{db: db} }

// Execute 校验并新增当前企业的联系人标签。
func (a *CreateTagAction) Execute(ctx context.Context, identity *servermodels.Identity, input TagInput) (*Tag, error) {
	input, codes := normalizeTagInput(input)
	if len(codes) > 0 {
		return nil, &common.FieldError{Fields: codes}
	}
	var tag *Tag
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		model := &servermodels.ContactTag{OrganizationID: identity.Organization.ID, Name: input.Name, AIInstruction: input.AIInstruction}
		_, err := tx.NewInsert().Model(model).Column("organization_id", "name", "ai_instruction").Returning("id").Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		tag, err = loadTag(ctx, tx, identity.Organization.ID, model.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create contact tag: %w", err)
	}
	return tag, nil
}

// UpdateTagAction 修改企业联系人标签。
type UpdateTagAction struct{ db *bun.DB }

// NewUpdateTagAction 创建联系人标签修改操作。
func NewUpdateTagAction(db *bun.DB) *UpdateTagAction { return &UpdateTagAction{db: db} }

// Execute 修改当前企业联系人标签的名称与 AI 添加条件，并通知拥有该标签的联系人所在客户端重读档案。
func (a *UpdateTagAction) Execute(ctx context.Context, identity *servermodels.Identity, tagID string, input TagInput) (*Tag, error) {
	if !common.ValidUUID(tagID) {
		return nil, ErrTagNotFound
	}
	input, codes := normalizeTagInput(input)
	if len(codes) > 0 {
		return nil, &common.FieldError{Fields: codes}
	}
	var tag *Tag
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ContactTag)(nil)).
			Set("name = ?", input.Name).
			Set("ai_instruction = ?", input.AIInstruction).
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, tagID).
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrTagNotFound
		}
		if err := touchTagContacts(ctx, tx, identity.Organization.ID, tagID); err != nil {
			return err
		}
		tag, err = loadTag(ctx, tx, identity.Organization.ID, tagID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update contact tag: %w", err)
	}
	return tag, nil
}

// DeleteTagAction 删除企业联系人标签。
type DeleteTagAction struct{ db *bun.DB }

// NewDeleteTagAction 创建联系人标签删除操作。
func NewDeleteTagAction(db *bun.DB) *DeleteTagAction { return &DeleteTagAction{db: db} }

// Execute 删除当前企业的联系人标签，移除所有联系人上的该标签并通知其客户端重读档案。
func (a *DeleteTagAction) Execute(ctx context.Context, identity *servermodels.Identity, tagID string) error {
	if !common.ValidUUID(tagID) {
		return ErrTagNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewDelete().Model((*servermodels.ContactTag)(nil)).
			Where("organization_id = ? AND id = ?", identity.Organization.ID, tagID).
			Exec(ctx)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrTagNotFound
		}
		if err := touchTagContacts(ctx, tx, identity.Organization.ID, tagID); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*servermodels.ContactTagAssignment)(nil)).
			Where("organization_id = ? AND tag_id = ?", identity.Organization.ID, tagID).
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete contact tag: %w", err)
	}
	return nil
}

// normalizeTagInput 规范化并校验标签名称与 AI 添加条件。
func normalizeTagInput(input TagInput) (TagInput, map[string]common.FieldCode) {
	codes := make(map[string]common.FieldCode)
	var code common.FieldCode
	if input.Name, code = normalizeName(input.Name, domain.ContactTagNameMaxLength); code != "" {
		codes["name"] = code
	}
	if input.AIInstruction, code = normalizeAIInstruction(input.AIInstruction); code != "" {
		codes["aiInstruction"] = code
	}
	return input, codes
}

// selectTags 构造当前企业联系人标签的查询。
func selectTags(db bun.IDB, organizationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("contact_tags AS ctg").
		ColumnExpr("ctg.id::text AS id, ctg.name, ctg.ai_instruction, ctg.created_at, ctg.updated_at").
		Where("ctg.organization_id = ?", organizationID)
}

// loadTag 读取当前企业的联系人标签。
func loadTag(ctx context.Context, db bun.IDB, organizationID, tagID string) (*Tag, error) {
	tag := &Tag{}
	err := selectTags(db, organizationID).Where("ctg.id = ?", tagID).Scan(ctx, tag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTagNotFound
	}
	return tag, err
}
