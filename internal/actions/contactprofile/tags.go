//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
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
	if err := selectTags(q.db, identity.Workspace.ID).OrderExpr("lower(ctg.name) ASC, ctg.id ASC").Scan(ctx, &tags); err != nil {
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
	input = normalizeTagInput(input)
	var tag *Tag
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		model := &servermodels.ContactTag{WorkspaceID: identity.Workspace.ID, Name: input.Name, AIInstruction: input.AIInstruction}
		_, err := tx.NewInsert().Model(model).Column("workspace_id", "name", "ai_instruction").Returning("id").Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		tag, err = loadTag(ctx, tx, identity.Workspace.ID, model.ID)
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
	input = normalizeTagInput(input)
	var tag *Tag
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ContactTag)(nil)).
			Set("name = ?", input.Name).
			Set("ai_instruction = ?", input.AIInstruction).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, tagID).
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
		if err := touchTagContacts(ctx, tx, identity.Workspace.ID, tagID); err != nil {
			return err
		}
		tag, err = loadTag(ctx, tx, identity.Workspace.ID, tagID)
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
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewDelete().Model((*servermodels.ContactTag)(nil)).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, tagID).
			Exec(ctx)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrTagNotFound
		}
		if err := touchTagContacts(ctx, tx, identity.Workspace.ID, tagID); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*servermodels.ContactTagAssignment)(nil)).
			Where("workspace_id = ? AND tag_id = ?", identity.Workspace.ID, tagID).
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete contact tag: %w", err)
	}
	return nil
}

// normalizeTagInput 去除标签名称与 AI 添加条件的首尾空白。
func normalizeTagInput(input TagInput) TagInput {
	input.Name = strings.TrimSpace(input.Name)
	input.AIInstruction = strings.TrimSpace(input.AIInstruction)
	return input
}

// selectTags 构造当前企业联系人标签的查询。
func selectTags(db bun.IDB, workspaceID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("contact_tags AS ctg").
		ColumnExpr("ctg.id::text AS id, ctg.name, ctg.ai_instruction, ctg.created_at, ctg.updated_at").
		Where("ctg.workspace_id = ?", workspaceID)
}

// loadTag 读取当前企业的联系人标签。
func loadTag(ctx context.Context, db bun.IDB, workspaceID, tagID string) (*Tag, error) {
	tag := &Tag{}
	err := selectTags(db, workspaceID).Where("ctg.id = ?", tagID).Scan(ctx, tag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTagNotFound
	}
	return tag, err
}
