//go:build server

// Package installation 实现平台首次安装的应用操作。
package installation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

// ErrAlreadyInstalled 表示平台已完成首次安装，首次安装入口关闭。
var ErrAlreadyInstalled = errors.New("platform is already installed")

// PlatformInitializer 在首次安装的事务内、平台行写入后初始化附加数据，失败时整个安装事务回滚。
type PlatformInitializer func(context.Context, bun.Tx) error

// InstallPlatformAction 创建平台管理员账号与默认工作区。
type InstallPlatformAction struct {
	db                  *bun.DB
	enqueuer            servertask.TxEnqueuer
	state               *deploymentaction.DeploymentState
	certificates        *certificateaction.Certificates
	initializer         workspaceaction.Initializer
	platformInitializer PlatformInitializer
}

// InstallPlatformInput 定义首次安装输入，PublicURL 是部署地址，WorkspaceName 是默认工作区名称。
type InstallPlatformInput struct {
	PublicURL     string
	WorkspaceName string
	DisplayName   string
	Email         string
	Password      string
	Locale        domain.Locale
	TimeZone      string
}

// InstallPlatformOutput 返回平台管理员与后台会话。
type InstallPlatformOutput struct {
	Account *servermodels.Account
	Session authaction.SessionOutput
}

// NewInstallPlatformAction 创建首次安装操作，platformInitializer 为空时不初始化附加数据。
func NewInstallPlatformAction(db *bun.DB, enqueuer servertask.TxEnqueuer, state *deploymentaction.DeploymentState, certificates *certificateaction.Certificates, initializer workspaceaction.Initializer, platformInitializer PlatformInitializer) *InstallPlatformAction {
	return &InstallPlatformAction{db: db, enqueuer: enqueuer, state: state, certificates: certificates, initializer: initializer, platformInitializer: platformInitializer}
}

// Execute 在平台尚未完成首次安装时写入平台行、创建默认工作区与平台管理员，并签发后台会话。
func (a *InstallPlatformAction) Execute(ctx context.Context, input InstallPlatformInput) (InstallPlatformOutput, error) {
	account := accountaction.NewAccountInput{
		DisplayName: strings.TrimSpace(input.DisplayName),
		Email:       strings.ToLower(strings.TrimSpace(input.Email)),
		Password:    input.Password,
		Locale:      input.Locale,
		TimeZone:    input.TimeZone,
	}
	fields := accountaction.ValidateNewAccount(account)
	workspaceName, workspaceFields := workspaceaction.NormalizeWorkspaceInput(workspaceaction.WorkspaceInput{Name: input.WorkspaceName})
	for _, code := range workspaceFields {
		fields["workspaceName"] = code
	}
	publicURL, validURL := certificateaction.NormalizePublicURL(input.PublicURL)
	if !validURL {
		fields["publicURL"] = certificateaction.ValidationPublicURLInvalid
	}
	if len(fields) > 0 {
		return InstallPlatformOutput{}, &common.FieldError{Fields: fields}
	}
	passwordHash, err := commonpassword.Hash(account.Password)
	if err != nil {
		return InstallPlatformOutput{}, fmt.Errorf("hash administrator password: %w", err)
	}

	var output InstallPlatformOutput
	err = a.certificates.WithLock(ctx, func(ctx context.Context) error {
		if _, err := platformaction.Load(ctx, a.db); !errors.Is(err, platformaction.ErrNotInstalled) {
			if err == nil {
				return ErrAlreadyInstalled
			}
			return err
		}
		certificate, certificateFields, err := a.certificates.Prepare(ctx, nil, publicURL, string(domain.CertificateSourceACME), "", "")
		if err != nil {
			return err
		}
		if len(certificateFields) > 0 {
			return &common.FieldError{Fields: certificateFields}
		}
		return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "LOCK TABLE platforms IN SHARE ROW EXCLUSIVE MODE"); err != nil {
				return err
			}
			installed, err := tx.NewSelect().Model((*servermodels.Platform)(nil)).Exists(ctx)
			if err != nil {
				return err
			}
			if installed {
				return ErrAlreadyInstalled
			}
			platform, err := platformaction.Create(ctx, tx, account.TimeZone, publicURL)
			if err != nil {
				return err
			}
			if _, err := a.enqueuer.EnqueueIn(ctx, licenseaction.RegisterDeploymentActionName, licenseaction.RegisterDeploymentInput{}, licenseaction.RegisterDeploymentEnqueueOptions); err != nil {
				return err
			}
			if certificate != nil {
				if err := certificate.Apply(ctx, tx, platform); err != nil {
					return err
				}
			}
			admin, err := accountaction.CreateAccount(ctx, tx, accountaction.NewAccount{
				Email: account.Email, PasswordHash: passwordHash, DisplayName: account.DisplayName,
				Locale: account.Locale, TimeZone: account.TimeZone, IsPlatformAdmin: true,
			})
			if err != nil {
				return err
			}
			created, err := workspaceaction.Create(ctx, tx, workspaceaction.CreateInput{
				Name: workspaceName.Name, Account: admin, AdminDisplayName: admin.DisplayName, Initializer: a.initializer,
			})
			if err != nil {
				return err
			}
			platform.DefaultWorkspaceID = created.Workspace.ID
			if _, err := tx.NewUpdate().Model(platform).Column("default_workspace_id").WherePK().Exec(ctx); err != nil {
				return err
			}
			session, err := authaction.IssueAdminSession(ctx, tx, admin)
			if err != nil {
				return err
			}
			output = InstallPlatformOutput{Account: admin, Session: session}
			if a.platformInitializer != nil {
				return a.platformInitializer(ctx, tx)
			}
			return nil
		})
	})
	var validationErr *common.FieldError
	var certificateErr *certificateaction.CertificateIssueError
	if errors.Is(err, ErrAlreadyInstalled) || errors.Is(err, certificateaction.ErrCertificateBusy) || errors.As(err, &validationErr) || errors.As(err, &certificateErr) {
		return InstallPlatformOutput{}, err
	}
	if err != nil {
		return InstallPlatformOutput{}, fmt.Errorf("install platform: %w", err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "平台首次安装完成", "public_url", publicURL)
	if err := a.state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "首次安装后刷新部署状态失败", "error", err)
	}
	return output, nil
}
