//go:build server

// Package installation 实现部署首次安装的应用操作。
package installation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	organizationaction "github.com/runforyou-ai/luway/internal/actions/organization"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	commonemail "github.com/runforyou-ai/luway/pkg/email"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

// ErrAlreadyInstalled 表示部署已完成首次安装，首次安装入口关闭。
var ErrAlreadyInstalled = errors.New("deployment is already installed")

// ValidationError 表示首次安装字段校验失败。
type ValidationError = common.FieldError

// InstallWorkspaceAction 创建部署管理员账号和第一个工作区。
type InstallWorkspaceAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// InstallWorkspaceInput 定义首次安装输入。
type InstallWorkspaceInput struct {
	WorkspaceName string
	WorkspaceSlug string
	DisplayName   string
	Email         string
	Password      string
	Locale        domain.Locale
	TimeZone      string
}

// InstallWorkspaceOutput 返回部署管理员的成员身份和登录会话。
type InstallWorkspaceOutput struct {
	Identity *servermodels.Identity
	Session  authaction.SessionOutput
}

// NewInstallWorkspaceAction 创建首次安装操作，enqueuer 投递实例与 control 的首次同步任务。
func NewInstallWorkspaceAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *InstallWorkspaceAction {
	return &InstallWorkspaceAction{db: db, enqueuer: enqueuer}
}

// Execute 在部署尚未完成首次安装时，于同一事务内生成部署实例并投递与 control 的首次同步，并创建部署管理员账号、第一个工作区和登录会话。
func (a *InstallWorkspaceAction) Execute(ctx context.Context, input InstallWorkspaceInput) (InstallWorkspaceOutput, error) {
	account := accountaction.NewAccountInput{
		DisplayName: strings.TrimSpace(input.DisplayName),
		Email:       commonemail.Normalize(input.Email),
		Password:    input.Password,
		Locale:      input.Locale,
		TimeZone:    input.TimeZone,
	}
	fields := accountaction.ValidateNewAccount(account)
	workspace, workspaceFields := organizationaction.NormalizeWorkspaceInput(organizationaction.WorkspaceInput{Name: input.WorkspaceName, Slug: input.WorkspaceSlug})
	// 工作区字段在安装表单中带 workspace 前缀。
	for field, code := range workspaceFields {
		fields["workspace"+strings.ToUpper(field[:1])+field[1:]] = code
	}
	if len(fields) > 0 {
		return InstallWorkspaceOutput{}, &ValidationError{Fields: fields}
	}
	passwordHash, err := commonpassword.Hash(account.Password)
	if err != nil {
		return InstallWorkspaceOutput{}, fmt.Errorf("hash administrator password: %w", err)
	}

	var output InstallWorkspaceOutput
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 锁定部署实例表后确认尚未写入部署实例，并发安装只有一个成功。
		if _, err := tx.ExecContext(ctx, "LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		installed, err := tx.NewSelect().Model((*servermodels.Deployment)(nil)).Exists(ctx)
		if err != nil {
			return err
		}
		if installed {
			return ErrAlreadyInstalled
		}
		if _, err := deploymentaction.Create(ctx, tx, a.enqueuer, account.TimeZone); err != nil {
			return err
		}
		admin, err := identityaction.CreateAccount(ctx, tx, identityaction.NewAccount{
			Email: account.Email, PasswordHash: passwordHash, DisplayName: account.DisplayName,
			Locale: account.Locale, TimeZone: account.TimeZone, IsDeploymentAdmin: true,
		})
		if err != nil {
			return err
		}
		identity, err := organizationaction.Create(ctx, tx, organizationaction.CreateInput{
			Name: workspace.Name, Slug: workspace.Slug, Account: admin, AdminDisplayName: account.DisplayName,
		})
		if err != nil {
			return err
		}
		session, err := authaction.IssueSession(ctx, tx, admin)
		if err != nil {
			return err
		}
		output = InstallWorkspaceOutput{Identity: identity, Session: session}
		return nil
	})
	if errors.Is(err, ErrAlreadyInstalled) {
		return InstallWorkspaceOutput{}, err
	}
	if err != nil {
		return InstallWorkspaceOutput{}, fmt.Errorf("install workspace: %w", err)
	}
	return output, nil
}
