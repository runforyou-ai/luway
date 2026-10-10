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

// ValidationError 表示首次安装字段校验失败。
type ValidationError = common.FieldError

// InstallWorkspaceAction 创建平台管理员账号和第一个工作区。
type InstallWorkspaceAction struct {
	db           *bun.DB
	enqueuer     servertask.TxEnqueuer
	state        *deploymentaction.DeploymentState
	certificates *certificateaction.Certificates
	initializer  workspaceaction.Initializer
}

// InstallWorkspaceInput 定义首次安装输入，PublicURL 是部署地址。
type InstallWorkspaceInput struct {
	PublicURL     string
	WorkspaceName string
	DisplayName   string
	Email         string
	Password      string
	Locale        domain.Locale
	TimeZone      string
}

// InstallWorkspaceOutput 返回平台管理员的成员身份和登录会话。
type InstallWorkspaceOutput struct {
	Identity *servermodels.Identity
	Session  authaction.SessionOutput
}

// NewInstallWorkspaceAction 创建首次安装操作，enqueuer 投递服务器与 control 的首次同步任务，state 在安装完成后刷新，certificates 为 HTTPS 部署地址签发证书。
func NewInstallWorkspaceAction(db *bun.DB, enqueuer servertask.TxEnqueuer, state *deploymentaction.DeploymentState, certificates *certificateaction.Certificates, initializer workspaceaction.Initializer) *InstallWorkspaceAction {
	return &InstallWorkspaceAction{db: db, enqueuer: enqueuer, state: state, certificates: certificates, initializer: initializer}
}

// Execute 在平台尚未完成首次安装时，于同一事务内写入带部署地址与证书的平台行并投递与 control 的首次同步，并创建平台管理员账号、第一个工作区和登录会话，完成后刷新本实例的部署状态。
func (a *InstallWorkspaceAction) Execute(ctx context.Context, input InstallWorkspaceInput) (InstallWorkspaceOutput, error) {
	account := accountaction.NewAccountInput{
		DisplayName: strings.TrimSpace(input.DisplayName),
		Email:       strings.ToLower(strings.TrimSpace(input.Email)),
		Password:    input.Password,
		Locale:      input.Locale,
		TimeZone:    input.TimeZone,
	}
	fields := accountaction.ValidateNewAccount(account)
	publicURL, validURL := certificateaction.NormalizePublicURL(input.PublicURL)
	if !validURL {
		fields["publicURL"] = certificateaction.ValidationPublicURLInvalid
	}
	workspace, workspaceFields := workspaceaction.NormalizeWorkspaceInput(workspaceaction.WorkspaceInput{Name: input.WorkspaceName})
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
	// 在部署证书锁内先为 HTTPS 部署地址签发证书，再在事务中写入平台行与证书。
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
			return &ValidationError{Fields: certificateFields}
		}
		return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
			// 锁定平台表后确认尚未写入平台行，并发安装只有一个成功。
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
			// 首次安装后与 control 完成首次同步。
			if _, err := a.enqueuer.EnqueueIn(ctx, licenseaction.SyncLicenseActionName, licenseaction.SyncLicenseInput{}, licenseaction.SyncLicenseEnqueueOptions); err != nil {
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
			identity, err := workspaceaction.Create(ctx, tx, workspaceaction.CreateInput{
				Name: workspace.Name, Account: admin, AdminDisplayName: account.DisplayName,
				Initializer: a.initializer,
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
	})
	var validationErr *ValidationError
	var certificateErr *certificateaction.CertificateIssueError
	if errors.Is(err, ErrAlreadyInstalled) || errors.Is(err, certificateaction.ErrCertificateBusy) || errors.As(err, &validationErr) || errors.As(err, &certificateErr) {
		return InstallWorkspaceOutput{}, err
	}
	if err != nil {
		return InstallWorkspaceOutput{}, fmt.Errorf("install workspace: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, output.Identity.Workspace.ID), "平台首次安装完成", "public_url", publicURL)
	// 安装已完成，刷新失败时由下次心跳刷新。
	if err := a.state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "首次安装后刷新部署状态失败", "error", err)
	}
	return output, nil
}
