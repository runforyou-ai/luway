//go:build server

package mcpserver

import (
	"context"
	"fmt"
	"slices"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ToolPurposeInput 定义一个工具的用途标记，用途为空表示取消标记。
type ToolPurposeInput struct {
	ToolName string
	Purpose  domain.MCPToolPurpose
}

// UpdateToolPurposeAction 标记 MCP 服务中一个工具的用途。
type UpdateToolPurposeAction struct{ db *bun.DB }

// NewUpdateToolPurposeAction 创建工具用途标记操作。
func NewUpdateToolPurposeAction(db *bun.DB) *UpdateToolPurposeAction {
	return &UpdateToolPurposeAction{db: db}
}

// Execute 在当前工具目录中标记或取消标记工具用途，不触发连接测试与目录更新。
func (a *UpdateToolPurposeAction) Execute(ctx context.Context, identity *servermodels.Identity, mcpServerID string, input ToolPurposeInput) (*Record, error) {
	if input.Purpose != "" && !input.Purpose.Valid() {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"purpose": ValidationToolPurposeInvalid}}
	}
	var mcpServer *servermodels.MCPServer
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		current, err := loadMCPServer(ctx, tx, identity.Organization.ID, mcpServerID, true)
		if err != nil {
			return err
		}
		// 只能标记当前目录中的工具。
		if !slices.ContainsFunc(current.Tools, func(item domain.MCPTool) bool { return item.Name == input.ToolName }) {
			return ErrToolNotFound
		}
		if current.ToolPurposes == nil {
			current.ToolPurposes = make(map[string]domain.MCPToolPurpose)
		}
		if input.Purpose == "" {
			delete(current.ToolPurposes, input.ToolName)
		} else {
			current.ToolPurposes[input.ToolName] = input.Purpose
		}
		if _, err := tx.NewUpdate().Model(current).Column("tool_purposes").Set("updated_at = now()").
			WherePK().Returning("*").Exec(ctx); err != nil {
			return err
		}
		mcpServer = current
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update MCP tool purpose: %w", err)
	}
	output := recordFromModel(*mcpServer)
	return &output, nil
}
