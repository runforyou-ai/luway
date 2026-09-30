//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	ValidationMemoryNameRequired        common.FieldCode = "ASSISTANT_MEMORY_NAME_REQUIRED"
	ValidationMemoryNameTooLong         common.FieldCode = "ASSISTANT_MEMORY_NAME_TOO_LONG"
	ValidationMemoryDescriptionRequired common.FieldCode = "ASSISTANT_MEMORY_DESCRIPTION_REQUIRED"
	ValidationMemoryDescriptionTooLong  common.FieldCode = "ASSISTANT_MEMORY_DESCRIPTION_TOO_LONG"
	ValidationMemoryBodyRequired        common.FieldCode = "ASSISTANT_MEMORY_BODY_REQUIRED"
	ValidationMemoryBodyTooLong         common.FieldCode = "ASSISTANT_MEMORY_BODY_TOO_LONG"
)

// ErrAssistantMemoryNotFound 表示助理不存在指定记忆。
var ErrAssistantMemoryNotFound = errors.New("assistant memory not found")

// AssistantMemory 定义助理的一条记忆。
type AssistantMemory struct {
	ID          string    `bun:"id"`
	Name        string    `bun:"name"`
	Description string    `bun:"description"`
	Body        string    `bun:"body"`
	UpdatedAt   time.Time `bun:"updated_at"`
}

// AssistantMemoryInput 定义主人编辑记忆时提交的名称、说明与正文。
type AssistantMemoryInput struct {
	Name        string
	Description string
	Body        string
}

// ListAssistantMemoriesQuery 读取当前成员名下助理的记忆。
type ListAssistantMemoriesQuery struct{ db *bun.DB }

// NewListAssistantMemoriesQuery 创建助理记忆列表查询。
func NewListAssistantMemoriesQuery(db *bun.DB) *ListAssistantMemoriesQuery {
	return &ListAssistantMemoriesQuery{db: db}
}

// Execute 按最近更新顺序返回当前成员名下助理的全部记忆。
func (q *ListAssistantMemoriesQuery) Execute(ctx context.Context, identity *servermodels.Identity, assistantID string) ([]AssistantMemory, error) {
	if _, err := loadAssistant(ctx, q.db, identity.Organization.ID, assistantID, identity.User.ID); err != nil {
		return nil, err
	}
	memories := make([]AssistantMemory, 0)
	if err := q.db.NewSelect().Model((*servermodels.AssistantMemory)(nil)).
		Column("id", "name", "description", "body", "updated_at").
		Where("am.organization_id = ? AND am.agent_id = ?", identity.Organization.ID, assistantID).
		OrderExpr("am.updated_at DESC, am.path ASC").
		Scan(ctx, &memories); err != nil {
		return nil, fmt.Errorf("list assistant memories: %w", err)
	}
	return memories, nil
}

// UpdateAssistantMemoryAction 由主人修改助理的一条记忆。
type UpdateAssistantMemoryAction struct{ db *bun.DB }

// NewUpdateAssistantMemoryAction 创建助理记忆编辑操作。
func NewUpdateAssistantMemoryAction(db *bun.DB) *UpdateAssistantMemoryAction {
	return &UpdateAssistantMemoryAction{db: db}
}

// Execute 校验并保存记忆的名称、说明与正文，内容变化时刷新更新时间并通知主人的其他客户端。
func (a *UpdateAssistantMemoryAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID, memoryID string, input AssistantMemoryInput) (*AssistantMemory, error) {
	input = AssistantMemoryInput{Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), Body: strings.TrimSpace(input.Body)}
	// 逐项校验必填与长度。
	fields := make(map[string]common.FieldCode)
	for _, check := range []struct {
		field, value      string
		max               int
		required, tooLong common.FieldCode
	}{
		{"name", input.Name, domain.AssistantMemoryNameMaxLength, ValidationMemoryNameRequired, ValidationMemoryNameTooLong},
		{"description", input.Description, domain.AssistantMemoryDescriptionMaxLength, ValidationMemoryDescriptionRequired, ValidationMemoryDescriptionTooLong},
		{"body", input.Body, domain.AssistantMemoryBodyMaxLength, ValidationMemoryBodyRequired, ValidationMemoryBodyTooLong},
	} {
		switch {
		case check.value == "":
			fields[check.field] = check.required
		case utf8.RuneCountInString(check.value) > check.max:
			fields[check.field] = check.tooLong
		}
	}
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	memory := &AssistantMemory{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockOwnAssistant(ctx, tx, identity, assistantID); err != nil {
			return err
		}
		if !common.ValidUUID(memoryID) {
			return ErrAssistantMemoryNotFound
		}
		result, err := tx.NewUpdate().Model((*servermodels.AssistantMemory)(nil)).
			Set("name = ?, description = ?, body = ?", input.Name, input.Description, input.Body).
			Set("updated_at = now()").
			Where("organization_id = ? AND agent_id = ? AND id = ?", identity.Organization.ID, assistantID, memoryID).
			Where("(name, description, body) IS DISTINCT FROM (?, ?, ?)", input.Name, input.Description, input.Body).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("update assistant memory: %w", err)
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			realtime.Notify(ctx, realtime.UserAssistantMemoryChanged(identity.Organization.ID, identity.User.ID, assistantID))
		}
		err = tx.NewSelect().Model((*servermodels.AssistantMemory)(nil)).
			Column("id", "name", "description", "body", "updated_at").
			Where("am.organization_id = ? AND am.agent_id = ? AND am.id = ?", identity.Organization.ID, assistantID, memoryID).
			Scan(ctx, memory)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAssistantMemoryNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return memory, nil
}

// DeleteAssistantMemoryAction 由主人删除助理的一条记忆。
type DeleteAssistantMemoryAction struct{ db *bun.DB }

// NewDeleteAssistantMemoryAction 创建助理记忆删除操作。
func NewDeleteAssistantMemoryAction(db *bun.DB) *DeleteAssistantMemoryAction {
	return &DeleteAssistantMemoryAction{db: db}
}

// Execute 删除当前成员名下助理的一条记忆并通知主人的其他客户端，记忆已不存在时视为删除成功。
func (a *DeleteAssistantMemoryAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID, memoryID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := lockOwnAssistant(ctx, tx, identity, assistantID); err != nil {
			return err
		}
		if !common.ValidUUID(memoryID) {
			return nil
		}
		result, err := tx.NewDelete().Model((*servermodels.AssistantMemory)(nil)).
			Where("organization_id = ? AND agent_id = ? AND id = ?", identity.Organization.ID, assistantID, memoryID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("delete assistant memory: %w", err)
		}
		if deleted, _ := result.RowsAffected(); deleted > 0 {
			realtime.Notify(ctx, realtime.UserAssistantMemoryChanged(identity.Organization.ID, identity.User.ID, assistantID))
		}
		return nil
	})
}
