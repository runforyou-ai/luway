//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrWorkspaceNotFound 表示支援目标工作区不存在。
var ErrWorkspaceNotFound = errors.New("workspace not found")

// SupportAccessOutput 返回支援会话与目标工作区。
type SupportAccessOutput struct {
	Session   SessionOutput
	Workspace servermodels.Workspace
}

// CreateSupportAccessAction 为平台管理员签发支援进入工作区的短期会话。
type CreateSupportAccessAction struct {
	db *bun.DB
}

// NewCreateSupportAccessAction 创建支援进入操作。
func NewCreateSupportAccessAction(db *bun.DB) *CreateSupportAccessAction {
	return &CreateSupportAccessAction{db: db}
}

// Execute 确保管理员在目标工作区拥有管理员成员身份后签发支援会话。
func (a *CreateSupportAccessAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, workspaceID string) (SupportAccessOutput, error) {
	if !str.IsUUID(workspaceID) {
		return SupportAccessOutput{}, ErrWorkspaceNotFound
	}
	var output SupportAccessOutput
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		workspace := &servermodels.Workspace{}
		err := tx.NewSelect().Model(workspace).Where("id = ?", workspaceID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkspaceNotFound
		}
		if err != nil {
			return err
		}
		if workspace.LifecycleStatus != string(domain.WorkspaceLifecycleActive) {
			return ErrWorkspaceSuspended
		}
		if err := ensureAdminMembership(ctx, tx, &operator.Account, workspace.ID); err != nil {
			return err
		}
		session, err := IssueSupportSession(ctx, tx, &operator.Account, workspace.ID)
		if err != nil {
			return err
		}
		output = SupportAccessOutput{Session: session, Workspace: *workspace}
		return nil
	})
	if err != nil {
		return SupportAccessOutput{}, err
	}
	return output, nil
}

// RevokeSupportAccessAction 撤销当前支援会话。
type RevokeSupportAccessAction struct {
	db *bun.DB
}

// NewRevokeSupportAccessAction 创建撤销支援会话操作。
func NewRevokeSupportAccessAction(db *bun.DB) *RevokeSupportAccessAction {
	return &RevokeSupportAccessAction{db: db}
}

// Execute 删除当前支援会话行。
func (a *RevokeSupportAccessAction) Execute(ctx context.Context, account *servermodels.AccountIdentity) error {
	if account.Session.Kind != string(domain.SessionKindSupport) {
		return ErrSessionKindMismatch
	}
	_, err := a.db.NewDelete().Model((*servermodels.AccountSession)(nil)).
		Where("id = ?", account.Session.ID).
		Where("account_id = ?", account.Account.ID).
		Where("kind = ?", domain.SessionKindSupport).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("revoke support session: %w", err)
	}
	return nil
}

// ensureAdminMembership 确保账号在工作区拥有有效的管理员成员身份；不计入席位上限。
func ensureAdminMembership(ctx context.Context, tx bun.Tx, account *servermodels.Account, workspaceID string) error {
	var adminRoleID string
	err := tx.NewSelect().Model((*servermodels.Role)(nil)).
		Column("id").
		Where("workspace_id = ?", workspaceID).
		Where("kind = ?", domain.RoleKindAdmin).
		Scan(ctx, &adminRoleID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("workspace admin role missing")
	}
	if err != nil {
		return err
	}

	user := &servermodels.User{}
	err = tx.NewSelect().Model(user).
		Where("workspace_id = ?", workspaceID).
		Where("account_id = ?", account.ID).
		For("UPDATE").
		Scan(ctx)
	if err == nil {
		if _, err := tx.NewUpdate().Model((*servermodels.User)(nil)).
			Set("status = ?", domain.IdentityStatusActive).
			Set("role_id = ?", adminRoleID).
			Where("id = ?", user.ID).
			Exec(ctx); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	displayName := strings.TrimSpace(account.DisplayName)
	if displayName == "" {
		displayName = account.Email
	}
	workspaceIdentity := &servermodels.WorkspaceIdentity{
		WorkspaceID:            workspaceID,
		Type:                   string(domain.WorkspaceIdentityTypeUser),
		DisplayName:            displayName,
		HandlesServiceRequests: false,
		WorkStatus:             string(domain.WorkStatusWorking),
	}
	if _, err := tx.NewInsert().Model(workspaceIdentity).
		Column("workspace_id", "type", "display_name", "handles_service_requests", "work_status").
		Returning("id").Exec(ctx); err != nil {
		return err
	}
	user = &servermodels.User{
		IdentityID:  workspaceIdentity.ID,
		WorkspaceID: workspaceID,
		AccountID:   account.ID,
		RoleID:      adminRoleID,
		Status:      string(domain.IdentityStatusActive),
	}
	_, err = tx.NewInsert().Model(user).
		Column("identity_id", "workspace_id", "account_id", "role_id", "status").
		Returning("id").Exec(ctx)
	return err
}
