//go:build server

package businesssystem

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateBusinessSystemAction 创建业务系统。
type CreateBusinessSystemAction struct {
	db   *bun.DB
	test *TestConnectionAction
}

// NewCreateBusinessSystemAction 创建业务系统创建操作。
func NewCreateBusinessSystemAction(db *bun.DB, test *TestConnectionAction) *CreateBusinessSystemAction {
	return &CreateBusinessSystemAction{db: db, test: test}
}

// Execute 在当前工作区中创建业务系统，并保存连接测试取得的工具目录。
func (a *CreateBusinessSystemAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	// 网络探测在写事务外执行，失败时不保存配置。
	connection, tools, err := a.test.Execute(ctx, input.Connection)
	if err != nil {
		return nil, err
	}
	var system servermodels.BusinessSystem
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		system = servermodels.BusinessSystem{
			WorkspaceID: identity.Workspace.ID, Name: input.Name, Transport: connection.Transport,
			Connection: connection.Connection, Credential: connection.Credential,
			HeaderBindings: input.HeaderBindings, Tools: tools,
		}
		_, err := tx.NewInsert().
			Model(&system).
			Column("workspace_id", "name", "transport", "connection", "credential", "header_bindings", "tools", "tools_updated_at").
			Value("tools_updated_at", "now()").
			Returning("*").
			Exec(ctx)
		return err
	})
	// 工作区内名称不区分大小写且保持唯一。
	if pgerr.UniqueViolationOn(err, "business_systems_workspace_name_unique") {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("create business system: %w", err)
	}
	output := recordFromModel(system)
	return &output, nil
}
