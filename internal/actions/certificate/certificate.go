//go:build server

// Package certificate 实现部署地址证书的校验、经 ACME 签发与定时续期，以及服务器命令行修改部署地址。
package certificate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	acmeintegration "github.com/runforyou-ai/luway/internal/integration/acme"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ValidationPublicURLInvalid 表示部署地址不是不带路径的 HTTP 或 HTTPS 地址。
const ValidationPublicURLInvalid common.FieldCode = "PLATFORM_PUBLIC_URL_INVALID"

// NormalizePublicURL 去掉部署地址的首尾空白与末尾的斜杠并转为小写，地址不是不带路径的 HTTP 或 HTTPS 地址时 valid 为假。
func NormalizePublicURL(input string) (publicURL string, valid bool) {
	publicURL = strings.ToLower(strings.TrimRight(strings.TrimSpace(input), "/"))
	return publicURL, str.IsHTTPOrigin(publicURL)
}

const (
	// RenewCertificateActionName 是检查并续期部署地址证书的 Action。
	RenewCertificateActionName = "platform.certificate.renew"
	// RenewCertificateScheduleKey 是检查并续期部署地址证书的定时计划标识。
	RenewCertificateScheduleKey = "platform-certificate-renew"

	// certificateRenewWindow 是自动签发的证书在到期前开始续期的时长。
	certificateRenewWindow = 30 * 24 * time.Hour
	// interactiveIssueTimeout 是保存部署地址时同步签发的时限，短于原生端与常见代理的请求时限。
	interactiveIssueTimeout = 20 * time.Second
	// renewIssueTimeout 是定时续期时一次签发的时限。
	renewIssueTimeout = 2 * time.Minute
)

// 部署地址证书的字段校验码。
const (
	ValidationCertificateSourceInvalid  common.FieldCode = "PLATFORM_CERTIFICATE_SOURCE_INVALID"
	ValidationCertificateInvalid        common.FieldCode = "PLATFORM_CERTIFICATE_INVALID"
	ValidationCertificateExpired        common.FieldCode = "PLATFORM_CERTIFICATE_EXPIRED"
	ValidationCertificateDomainMismatch common.FieldCode = "PLATFORM_CERTIFICATE_DOMAIN_MISMATCH"
	ValidationPublicURLDomainRequired   common.FieldCode = "PLATFORM_PUBLIC_URL_DOMAIN_REQUIRED"
)

// ErrCertificateBusy 表示部署中正在签发证书，本次保存未执行。
var ErrCertificateBusy = errors.New("deployment certificate is being issued")

// CertificateIssueError 表示自动签发部署地址的证书失败，Reason 是失败原因：ACME 服务给出的说明，没有说明时为原始错误。
type CertificateIssueError struct {
	Domain string
	Reason string
	Err    error
}

// Error 返回签发失败的说明。
func (e *CertificateIssueError) Error() string {
	return fmt.Sprintf("issue certificate for %s: %v", e.Domain, e.Err)
}

// Unwrap 返回签发失败的原始错误。
func (e *CertificateIssueError) Unwrap() error {
	return e.Err
}

// CertificateIssuer 经 ACME 服务为域名签发证书。
type CertificateIssuer interface {
	Issue(ctx context.Context, accountKey, domain string) (acmeintegration.Certificate, error)
}

// DeploymentCertificate 定义部署地址的证书：来源、PEM 证书链与私钥、到期时间，以及最近一次自动签发失败的原因与时间。
type DeploymentCertificate struct {
	Source          string
	Chain           string
	PrivateKey      string
	ExpiresAt       *time.Time
	RenewalError    string
	RenewalFailedAt *time.Time
}

// FromPlatform 读取平台行中的证书。
func FromPlatform(platform *servermodels.Platform) DeploymentCertificate {
	return DeploymentCertificate{
		Source: platform.CertificateSource, Chain: platform.Certificate, PrivateKey: platform.CertificatePrivateKey,
		ExpiresAt: platform.CertificateExpiresAt, RenewalError: platform.CertificateRenewalError,
		RenewalFailedAt: platform.CertificateRenewalFailedAt,
	}
}

// CertificateChange 是保存部署地址时写入平台行的证书；issued 为真时同时写入签发证书使用的 ACME 账号私钥。
type CertificateChange struct {
	source     string
	chain      string
	privateKey string
	expiresAt  time.Time
	accountKey string
	issued     bool
}

// Apply 把证书写入平台行并清除自动签发失败记录。
func (c *CertificateChange) Apply(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
	platform.CertificateSource, platform.Certificate, platform.CertificatePrivateKey = c.source, c.chain, c.privateKey
	platform.CertificateExpiresAt, platform.CertificateRenewalError, platform.CertificateRenewalFailedAt = &c.expiresAt, "", nil
	columns := []string{"certificate_source", "certificate", "certificate_private_key", "certificate_expires_at", "certificate_renewal_error", "certificate_renewal_failed_at"}
	if c.issued {
		platform.ACMEAccountKey = c.accountKey
		columns = append(columns, "acme_account_key")
	}
	_, err := tx.NewUpdate().Model(platform).Column(columns...).WherePK().Exec(ctx)
	return err
}

// Certificates 为部署地址准备、签发与续期证书，同一时刻部署中只有一处在签发。
type Certificates struct {
	db     *bun.DB
	issuer CertificateIssuer
}

// NewCertificates 创建部署地址的证书管理，issuer 经 ACME 服务签发证书。
func NewCertificates(db *bun.DB, issuer CertificateIssuer) *Certificates {
	return &Certificates{db: db, issuer: issuer}
}

// ParseCertificate 解析 PEM 证书链与私钥，返回可用于 TLS 服务的证书，其 Leaf 为第一张证书。
func ParseCertificate(chain, privateKey string) (*tls.Certificate, error) {
	certificate, err := tls.X509KeyPair([]byte(chain), []byte(privateKey))
	if err != nil {
		return nil, err
	}
	if certificate.Leaf == nil {
		if certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0]); err != nil {
			return nil, err
		}
	}
	return &certificate, nil
}

// Prepare 按保存后的部署地址与证书来源准备要写入的证书：部署地址为 HTTP 时不修改证书；上传的证书须与私钥匹配、未到期且包含部署地址的主机名；自动签发时，部署地址为 HTTPS 且部署中有直接提供 HTTPS 的服务器，而 current 中没有该主机名的有效自动签发证书时立即签发。current 为空表示平台尚未安装；返回空值表示不修改证书。
func (c *Certificates) Prepare(ctx context.Context, current *servermodels.Platform, publicURL, source, chain, privateKey string) (*CertificateChange, map[string]common.FieldCode, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil {
		return nil, nil, err
	}
	host := parsed.Hostname()
	switch source {
	case string(domain.CertificateSourceUpload), string(domain.CertificateSourceACME):
	default:
		return nil, map[string]common.FieldCode{"certificateSource": ValidationCertificateSourceInvalid}, nil
	}
	if parsed.Scheme != "https" {
		return nil, nil, nil
	}
	if source == string(domain.CertificateSourceUpload) {
		certificate, err := ParseCertificate(chain, privateKey)
		if err != nil {
			return nil, map[string]common.FieldCode{"certificate": ValidationCertificateInvalid}, nil
		}
		// 上传证书的有效期由签发机构给出。
		if !time.Now().Before(certificate.Leaf.NotAfter) { //clock:local
			return nil, map[string]common.FieldCode{"certificate": ValidationCertificateExpired}, nil
		}
		if certificate.Leaf.VerifyHostname(host) != nil {
			return nil, map[string]common.FieldCode{"certificate": ValidationCertificateDomainMismatch}, nil
		}
		return &CertificateChange{source: source, chain: chain, privateKey: privateKey, expiresAt: certificate.Leaf.NotAfter}, nil, nil
	}
	online, err := serverinstanceaction.HTTPSServersOnline(ctx, c.db)
	if err != nil || !online {
		return nil, nil, err
	}
	if !acmeDomain(host) {
		return nil, map[string]common.FieldCode{"publicURL": ValidationPublicURLDomainRequired}, nil
	}
	if current != nil && current.CertificateSource == string(domain.CertificateSourceACME) {
		valid, err := c.certificateValid(ctx, current, host)
		if err != nil || valid {
			return nil, nil, err
		}
	}
	accountKey := ""
	if current != nil {
		accountKey = current.ACMEAccountKey
	}
	change, err := c.issue(ctx, accountKey, host, interactiveIssueTimeout)
	return change, nil, err
}

// certificateValid 判断平台行中的证书包含 host 且距到期超过续期时长。
func (c *Certificates) certificateValid(ctx context.Context, platform *servermodels.Platform, host string) (bool, error) {
	certificate, err := ParseCertificate(platform.Certificate, platform.CertificatePrivateKey)
	if err != nil || certificate.Leaf.VerifyHostname(host) != nil {
		return false, nil
	}
	var fresh bool
	if err := c.db.NewRaw("SELECT ?::timestamptz > now() + make_interval(secs => ?)", certificate.Leaf.NotAfter, certificateRenewWindow.Seconds()).
		Scan(ctx, &fresh); err != nil {
		return false, fmt.Errorf("check certificate expiry: %w", err)
	}
	return fresh, nil
}

// issue 在 timeout 内用 accountKey 对应的 ACME 账号为 host 签发证书，accountKey 为空时生成新账号私钥。
func (c *Certificates) issue(ctx context.Context, accountKey, host string, timeout time.Duration) (*CertificateChange, error) {
	if accountKey == "" {
		var err error
		if accountKey, err = acmeintegration.NewAccountKey(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	certificate, err := c.issuer.Issue(ctx, accountKey, host)
	if err != nil {
		reason := acmeintegration.Reason(err)
		if reason == "" {
			reason = err.Error()
		}
		return nil, &CertificateIssueError{Domain: host, Reason: reason, Err: err}
	}
	return &CertificateChange{
		source: string(domain.CertificateSourceACME), chain: certificate.Chain, privateKey: certificate.PrivateKey,
		expiresAt: certificate.ExpiresAt, accountKey: accountKey, issued: true,
	}, nil
}

// WithLock 在部署证书咨询锁内执行 action，部署中正在签发证书时不等待，返回 ErrCertificateBusy。
func (c *Certificates) WithLock(ctx context.Context, action func(ctx context.Context) error) error {
	return c.withLock(ctx, false, action)
}

// withLock 在部署证书咨询锁内执行 action；wait 为真时等待正在进行的签发结束，否则锁被占用时返回 ErrCertificateBusy。
func (c *Certificates) withLock(ctx context.Context, wait bool, action func(ctx context.Context) error) error {
	run := func(bun.Conn) error { return action(ctx) }
	if wait {
		return serverstorage.WithSessionLock(ctx, c.db, serverstorage.LockDeploymentCertificate, nil, run)
	}
	locked, err := serverstorage.TryWithSessionLock(ctx, c.db, serverstorage.LockDeploymentCertificate, "", run)
	if err == nil && !locked {
		return ErrCertificateBusy
	}
	return err
}

// Renew 在部署地址为 HTTPS、证书自动签发且部署中有直接提供 HTTPS 的服务器时，为缺少有效证书或证书临近到期的部署地址签发证书；签发失败时记录原因，由下次定时检查重试。
func (c *Certificates) Renew(ctx context.Context, _ struct{}) error {
	return c.withLock(ctx, true, func(ctx context.Context) error {
		platform, err := platformaction.Load(ctx, c.db)
		if errors.Is(err, platformaction.ErrNotInstalled) {
			return nil
		}
		if err != nil {
			return err
		}
		parsed, err := url.Parse(platform.PublicURL)
		if err != nil || parsed.Scheme != "https" || platform.CertificateSource != string(domain.CertificateSourceACME) || !acmeDomain(parsed.Hostname()) {
			return nil
		}
		online, err := serverinstanceaction.HTTPSServersOnline(ctx, c.db)
		if err != nil || !online {
			return err
		}
		host := parsed.Hostname()
		if valid, err := c.certificateValid(ctx, platform, host); err != nil || valid {
			return err
		}
		change, issueErr := c.issue(ctx, platform.ACMEAccountKey, host, renewIssueTimeout)
		return serverstorage.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := platformaction.Lock(ctx, tx)
			if err != nil {
				return err
			}
			// 签发期间部署地址或证书来源已修改时放弃本次结果。
			if locked.PublicURL != platform.PublicURL || locked.CertificateSource != string(domain.CertificateSourceACME) {
				return nil
			}
			if issueErr == nil {
				slog.InfoContext(ctx, "已签发部署地址证书", "domain", host, "expires_at", change.expiresAt)
				return change.Apply(ctx, tx, locked)
			}
			slog.WarnContext(ctx, "签发部署地址证书失败", "domain", host, "error", issueErr)
			reason := issueErr.Error()
			if certificateErr, ok := errors.AsType[*CertificateIssueError](issueErr); ok {
				reason = certificateErr.Reason
			}
			_, err = tx.NewUpdate().Model(locked).
				Set("certificate_renewal_error = ?", reason).
				Set("certificate_renewal_failed_at = now()").
				WherePK().Exec(ctx)
			return err
		})
	})
}

// acmeDomain 判断 host 是可经 ACME 签发证书的公网域名。
func acmeDomain(host string) bool {
	host = strings.ToLower(host)
	return net.ParseIP(host) == nil && strings.Contains(host, ".") && host != "localhost" && !strings.HasSuffix(host, ".localhost")
}

// SetPublicURL 修改部署地址并沿用当前的证书来源与上传的证书，自动签发时按需先签发证书；服务器命令行在管理端无法访问时使用，部署中的服务器在下次心跳时生效。
func (c *Certificates) SetPublicURL(ctx context.Context, input string) (map[string]common.FieldCode, error) {
	publicURL, valid := NormalizePublicURL(input)
	if !valid {
		return map[string]common.FieldCode{"publicURL": ValidationPublicURLInvalid}, nil
	}
	var fields map[string]common.FieldCode
	err := c.WithLock(ctx, func(ctx context.Context) error {
		current, err := platformaction.Load(ctx, c.db)
		if err != nil {
			return err
		}
		change, prepareFields, err := c.Prepare(ctx, current, publicURL, current.CertificateSource, current.Certificate, current.CertificatePrivateKey)
		if err != nil || len(prepareFields) > 0 {
			fields = prepareFields
			return err
		}
		return serverstorage.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
			platform, err := platformaction.Lock(ctx, tx)
			if err != nil {
				return err
			}
			platform.PublicURL = publicURL
			if _, err := tx.NewUpdate().Model(platform).Column("public_url").WherePK().Exec(ctx); err != nil {
				return err
			}
			if change == nil {
				return nil
			}
			return change.Apply(ctx, tx, platform)
		})
	})
	if err == nil && len(fields) == 0 {
		slog.InfoContext(ctx, "已修改部署地址", "public_url", publicURL)
	}
	return fields, err
}
