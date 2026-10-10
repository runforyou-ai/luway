//go:build server

// Package deployment 实现整个部署共用的部署配置的读取与修改，以及本进程缓存的部署状态快照。
package deployment

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/uptrace/bun"
)

// DeploymentSnapshot 是一次读取的部署状态：部署版本、部署配置、注册策略、上报开关与平台生效的能力。
type DeploymentSnapshot struct {
	// Installed 表示平台已完成首次安装；未安装时部署配置为空、上报关闭。
	Installed bool
	// Version 是部署当前运行的服务端版本。
	Version string
	// Settings 是平台行中的部署配置。
	Settings DeploymentSettings
	// IconDigest 是网站图标内容的 SHA-256 摘要（十六进制），没有网站图标时为空。
	IconDigest string
	// TelemetryEnabled 表示是否向 control 上报运行指标与错误。
	TelemetryEnabled bool
	// RegistrationPolicy 是平台注册策略。
	RegistrationPolicy domain.RegistrationPolicy
	// Capabilities 是读取时授权生效的能力，授权是否到期按数据库时刻判断。
	Capabilities domain.Capabilities
	// TLSCertificate 是由部署地址证书解析出的 TLS 证书，没有证书或证书无法解析时为空。
	TLSCertificate *tls.Certificate
}

// DeploymentState 缓存本进程使用的部署状态快照，由心跳定期刷新，本实例修改部署配置、上报开关或授权后立即刷新。
type DeploymentState struct {
	db      bun.IDB
	current atomic.Pointer[DeploymentSnapshot]
	// mu 串行化刷新，快照按读取顺序替换。
	mu sync.Mutex
}

// NewDeploymentState 创建部署状态，首次刷新前为未安装的空快照。
func NewDeploymentState(db bun.IDB) *DeploymentState {
	state := &DeploymentState{db: db}
	state.current.Store(&DeploymentSnapshot{})
	return state
}

// Current 返回当前的部署状态快照，调用方不得修改其中的名称表与图标。
func (s *DeploymentState) Current() DeploymentSnapshot {
	return *s.current.Load()
}

// PublicURL 返回当前的部署地址。
func (s *DeploymentState) PublicURL() string {
	return s.current.Load().Settings.PublicURL
}

// S3 返回当前的对象存储配置。
func (s *DeploymentState) S3() filecontent.S3Config {
	return s.current.Load().Settings.S3
}

// TelemetryEnabled 返回当前是否向 control 上报运行指标与错误。
func (s *DeploymentState) TelemetryEnabled() bool {
	return s.current.Load().TelemetryEnabled
}

// BrandOverride 返回部署品牌的覆盖值与是否生效，作为当前品牌的覆盖来源。
func (s *DeploymentState) BrandOverride() (brand.Override, bool) {
	current := s.current.Load()
	return current.Settings.Branding.Override(), current.Capabilities.CustomBranding
}

// TLSCertificate 返回直接提供 HTTPS 时为 HTTPS 部署地址使用的证书，没有可用证书时为空。
func (s *DeploymentState) TLSCertificate() *tls.Certificate {
	return s.current.Load().TLSCertificate
}

// Capabilities 返回最近一次刷新时授权生效的能力；未安装时为零值。
func (s *DeploymentState) Capabilities() domain.Capabilities {
	return s.current.Load().Capabilities
}

// Reload 读取部署版本、平台行与授权生效的能力并替换快照；网站图标只在内容变化时重新读取，部署地址证书只在内容变化时重新解析。
func (s *DeploymentState) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.current.Load()
	snapshot := DeploymentSnapshot{}
	version, err := serverinstanceaction.DeploymentVersion(ctx, s.db)
	if err != nil {
		return err
	}
	snapshot.Version = version
	platform, err := platformaction.Load(ctx, s.db)
	if err != nil && !errors.Is(err, platformaction.ErrNotInstalled) {
		return fmt.Errorf("load platform: %w", err)
	}
	if platform != nil {
		snapshot.Installed = true
		snapshot.Settings = deploymentSettingsFromModel(platform)
		snapshot.TelemetryEnabled = platform.TelemetryEnabled
		snapshot.RegistrationPolicy = domain.RegistrationPolicy(platform.RegistrationPolicy)
		if err := s.db.NewSelect().Model((*servermodels.Platform)(nil)).
			ColumnExpr("coalesce(encode(sha256(brand_icon), 'hex'), '')").Limit(1).Scan(ctx, &snapshot.IconDigest); err != nil {
			return fmt.Errorf("load brand icon digest: %w", err)
		}
		snapshot.Settings.Branding.Icon = previous.Settings.Branding.Icon
		if snapshot.IconDigest != previous.IconDigest {
			snapshot.Settings.Branding.Icon = nil
			if err := s.db.NewSelect().Model((*servermodels.Platform)(nil)).Column("brand_icon").Limit(1).Scan(ctx, &snapshot.Settings.Branding.Icon); err != nil {
				return fmt.Errorf("load brand icon: %w", err)
			}
		}
		if snapshot.Capabilities, err = licenseaction.Capabilities(ctx, s.db); err != nil {
			return err
		}
		// 证书内容未变化时沿用已解析的证书。
		certificate := snapshot.Settings.Certificate
		if certificate.Chain == previous.Settings.Certificate.Chain && certificate.PrivateKey == previous.Settings.Certificate.PrivateKey {
			snapshot.TLSCertificate = previous.TLSCertificate
		} else if certificate.Chain != "" {
			if snapshot.TLSCertificate, err = certificateaction.ParseCertificate(certificate.Chain, certificate.PrivateKey); err != nil {
				slog.WarnContext(ctx, "解析部署地址证书失败", "error", err)
			}
		}
	}
	s.current.Store(&snapshot)
	return nil
}

// Mailer 返回按当前 SMTP 配置发送邮件的发送器。
func (s *DeploymentState) Mailer() *DeploymentMailer {
	return &DeploymentMailer{state: s}
}

// ErrMailDisabled 表示部署未配置 SMTP 主机。
var ErrMailDisabled = errors.New("mail delivery is not configured")

// DeploymentMailer 按发送时的部署 SMTP 配置发送邮件。
type DeploymentMailer struct {
	state *DeploymentState
}

// Enabled 判断部署是否配置了 SMTP 主机。
func (m *DeploymentMailer) Enabled() bool {
	return m.state.current.Load().Settings.SMTP.Host != ""
}

// Send 用当前 SMTP 配置建立一次连接并投递邮件，未配置时返回 ErrMailDisabled。
func (m *DeploymentMailer) Send(ctx context.Context, message mail.Message) error {
	config := m.state.current.Load().Settings.SMTP
	if config.Host == "" {
		return ErrMailDisabled
	}
	return mail.NewClient(config).Send(ctx, message)
}
