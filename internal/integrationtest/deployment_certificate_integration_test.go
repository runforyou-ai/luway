//go:build server

package integrationtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"
	"uuid"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	acmeintegration "github.com/runforyou-ai/luway/internal/integration/acme"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

// fakeIssuer 以自签名证书模拟 ACME 签发，记录每次成功签发的域名与账号私钥；err 非空时签发失败。
type fakeIssuer struct {
	mu          sync.Mutex
	domains     []string
	accountKeys []string
	validity    time.Duration
	err         error
}

// Issue 为 domain 生成有效期为 validity 的自签名证书。
func (f *fakeIssuer) Issue(_ context.Context, accountKey, domain string) (acmeintegration.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return acmeintegration.Certificate{}, f.err
	}
	f.domains = append(f.domains, domain)
	f.accountKeys = append(f.accountKeys, accountKey)
	chain, key, expiresAt := selfSignedCertificate(nil, domain, time.Now().Add(f.validity))
	return acmeintegration.Certificate{Chain: chain, PrivateKey: key, ExpiresAt: expiresAt}, nil
}

// issued 返回成功签发的域名。
func (f *fakeIssuer) issued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.domains...)
}

// selfSignedCertificate 生成包含 domain、在 notAfter 到期的自签名 PEM 证书与私钥。
func selfSignedCertificate(t *testing.T, domain string, notAfter time.Time) (chain, privateKey string, expiresAt time.Time) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if t != nil {
		require.NoError(t, err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter.Truncate(time.Second),
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), template.NotAfter
}

// TestDeploymentCertificate 验证部署地址证书：上传的证书须与私钥匹配、未到期且包含 HTTPS 部署地址域名，HTTP 部署地址不校验证书；有直接提供 HTTPS 的服务器时 HTTPS 部署地址自动签发证书，
// 已有有效证书时沿用，签发失败时不保存并返回失败原因，正在签发时不等待；定时检查为临近到期的证书续期，失败时记录原因；HTTP-01 质询应答经数据库共享。
func TestDeploymentCertificate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	deployment := servertest.NewDeployment(t, db)
	issuer := &fakeIssuer{validity: 90 * 24 * time.Hour}
	service := direct.New(db, direct.DeploymentConfig{Deployment: deployment, CertificateIssuer: issuer}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: "http://203.0.113.5", WorkspaceName: "证书", DisplayName: "管理员", Email: servertest.UniqueEmail("certificate"),
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: installed.Auth.Token, Locale: domain.LocaleChineseSimplified}
	upload := func(publicURL, chain, key string) (appservice.PlatformDeployment, error) {
		return service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{
			PublicURL: publicURL, Certificate: appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceUpload, Certificate: chain, PrivateKey: key},
		})
	}

	// 上传的证书须能与私钥配对、未到期且包含 HTTPS 部署地址的域名。
	chain, key, expiresAt := selfSignedCertificate(t, "upload.example.test", time.Now().Add(30*24*time.Hour))
	otherChain, otherKey, _ := selfSignedCertificate(t, "upload.example.test", time.Now().Add(30*24*time.Hour))
	_, err = upload("https://upload.example.test", chain, otherKey)
	servertest.RequireFieldError(t, err, "certificate", i18n.FieldCertificateInvalid)
	expiredChain, expiredKey, _ := selfSignedCertificate(t, "upload.example.test", time.Now().Add(-time.Hour))
	_, err = upload("https://upload.example.test", expiredChain, expiredKey)
	servertest.RequireFieldError(t, err, "certificate", i18n.FieldCertificateExpired)
	_, err = upload("https://other.example.test", chain, key)
	servertest.RequireFieldError(t, err, "certificate", i18n.FieldCertificateDomainMismatch)
	_, err = service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{PublicURL: "https://upload.example.test", Certificate: appservice.PlatformCertificate{Source: "manual"}})
	servertest.RequireFieldError(t, err, "certificateSource", i18n.FieldCertificateSourceInvalid)
	updated, err := upload("https://upload.example.test", "\n"+otherChain+"\n", otherKey)
	require.NoError(t, err)
	require.Equal(t, appservice.PlatformCertificateSourceUpload, updated.Certificate.Source)
	require.Equal(t, []string{"upload.example.test"}, updated.CertificateStatus.Domains)
	require.NotEmpty(t, updated.Certificate.PrivateKey)
	require.NotNil(t, deployment.TLSCertificate())
	require.Equal(t, "upload.example.test", deployment.TLSCertificate().Leaf.DNSNames[0])
	updated, err = upload("https://upload.example.test", chain, key)
	require.NoError(t, err)
	require.WithinDuration(t, expiresAt, *updated.CertificateStatus.ExpiresAt, time.Second)
	require.Empty(t, issuer.issued(), "uploaded certificates are not issued")

	// HTTP 部署地址不校验证书，也不修改已保存的证书来源与证书。
	updated, err = upload("http://upload.example.test", "", "")
	require.NoError(t, err)
	require.Equal(t, "http://upload.example.test", updated.PublicURL)
	require.WithinDuration(t, expiresAt, *updated.CertificateStatus.ExpiresAt, time.Second)
	updated, err = service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{PublicURL: "http://upload.example.test", Certificate: appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceACME}})
	require.NoError(t, err)
	require.Equal(t, appservice.PlatformCertificateSourceUpload, updated.Certificate.Source)

	// 部署中没有直接提供 HTTPS 的服务器时，自动签发不签发证书。
	acmeAddress := func(publicURL string) (appservice.PlatformDeployment, error) {
		return service.UpdatePlatformAddress(ctx, adminMeta, appservice.PlatformAddressInput{PublicURL: publicURL, Certificate: appservice.PlatformCertificate{Source: appservice.PlatformCertificateSourceACME}})
	}
	updated, err = acmeAddress("https://acme.example.test")
	require.NoError(t, err)
	require.False(t, updated.HTTPSServers)
	require.Empty(t, updated.Certificate.Certificate, "automatic certificates are not returned")
	require.Empty(t, issuer.issued())

	// 有直接提供 HTTPS 的服务器时，IP 地址不能自动签发，域名在保存前签发证书。
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{
		ID: uuid.NewV7().String(), Hostname: "edge", Version: buildinfo.Version, Config: serverconfig.Diagnostics{HTTPSPort: 443},
	}))
	_, err = acmeAddress("https://203.0.113.5")
	servertest.RequireFieldError(t, err, "publicURL", i18n.FieldPublicURLDomainRequired)
	updated, err = acmeAddress("https://acme.example.test")
	require.NoError(t, err)
	require.True(t, updated.HTTPSServers)
	require.Equal(t, []string{"acme.example.test"}, updated.CertificateStatus.Domains)
	require.Equal(t, []string{"acme.example.test"}, issuer.issued())
	require.Equal(t, "acme.example.test", deployment.TLSCertificate().Leaf.DNSNames[0])
	var accountKey string
	require.NoError(t, db.NewRaw("SELECT acme_account_key FROM platforms").Scan(ctx, &accountKey))
	require.Equal(t, issuer.accountKeys[0], accountKey)

	// 已有有效的自动签发证书时沿用，HTTP 部署地址不签发。
	_, err = acmeAddress("https://acme.example.test/")
	require.NoError(t, err)
	_, err = acmeAddress("http://acme.example.test")
	require.NoError(t, err)
	require.Len(t, issuer.issued(), 1)

	// 签发失败时不保存部署地址，并返回 ACME 服务给出的说明。
	issuer.err = &acme.AuthorizationError{Identifier: "fail.example.test", Errors: []error{&acme.Error{Detail: "connection refused"}}}
	_, err = acmeAddress("https://fail.example.test")
	appErr, ok := errors.AsType[*appservice.Error](err)
	require.True(t, ok, "error = %v", err)
	require.Equal(t, i18n.LocalizeTemplate("zh-CN", i18n.ErrorCertificateIssueFailed, map[string]any{"Reason": "connection refused"}), appErr.Message)
	require.Equal(t, "http://acme.example.test", deployment.PublicURL())
	issuer.err = fmt.Errorf("wait ACME order: %w", context.DeadlineExceeded)
	_, err = acmeAddress("https://fail.example.test")
	servertest.RequireLocalizedError(t, err, i18n.ErrorCertificateIssueTimeout)

	// 定时检查为临近到期的证书续期，账号私钥沿用；签发失败时记录原因。
	issuer.err = nil
	_, err = acmeAddress("https://acme.example.test")
	require.NoError(t, err)
	certificates := certificateaction.NewCertificates(db, issuer)
	require.NoError(t, certificates.Renew(ctx, struct{}{}))
	require.Len(t, issuer.issued(), 1, "fresh certificates are not renewed")
	soonChain, soonKey, _ := selfSignedCertificate(t, "acme.example.test", time.Now().Add(10*24*time.Hour))
	_, err = db.NewRaw("UPDATE platforms SET certificate = ?, certificate_private_key = ?", soonChain, soonKey).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, certificates.Renew(ctx, struct{}{}))
	require.Len(t, issuer.issued(), 2)
	require.Equal(t, accountKey, issuer.accountKeys[1])
	_, err = db.NewRaw("UPDATE platforms SET certificate = ?, certificate_private_key = ?", soonChain, soonKey).Exec(ctx)
	require.NoError(t, err)
	issuer.err = &acme.Error{Detail: "rate limited"}
	require.NoError(t, certificates.Renew(ctx, struct{}{}))
	current, err := service.GetPlatformDeployment(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, "rate limited", current.CertificateStatus.RenewalError)
	require.NotNil(t, current.CertificateStatus.RenewalFailedAt)

	// 部署中正在签发证书时，保存部署地址不等待，提示稍后重试。
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	err = serverstorage.SessionLock(ctx, conn, serverstorage.LockDeploymentCertificate)
	require.NoError(t, err)
	_, err = acmeAddress("https://acme.example.test")
	servertest.RequireLocalizedError(t, err, i18n.ErrorCertificateBusy)
	err = serverstorage.SessionUnlock(ctx, conn, serverstorage.LockDeploymentCertificate)
	require.NoError(t, err)

	// HTTP-01 质询应答写入数据库后可按令牌读取，撤销后读取不到。
	challenges := serverstorage.NewACMEChallenges(db)
	require.NoError(t, challenges.Put(ctx, "token-1", "token-1.key"))
	value, found, err := challenges.KeyAuthorization(ctx, "token-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "token-1.key", value)
	require.NoError(t, challenges.Delete(ctx, "token-1"))
	_, found, err = challenges.KeyAuthorization(ctx, "token-1")
	require.NoError(t, err)
	require.False(t, found)
}
