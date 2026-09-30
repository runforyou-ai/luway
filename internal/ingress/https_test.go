//go:build server

package ingress

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"golang.org/x/crypto/acme/autocert"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip 执行测试指定的 HTTP 传输。
func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestRequestHostKeepsLocalAddressesOnHTTP 验证本地和内网地址使用 HTTP。
func TestRequestHostKeepsLocalAddressesOnHTTP(t *testing.T) {
	tests := []struct {
		value string
		host  string
		local bool
	}{
		{value: "localhost", host: "localhost", local: true},
		{value: "localhost:8080", host: "localhost", local: true},
		{value: "127.0.0.1:8080", host: "127.0.0.1", local: true},
		{value: "[::1]:8080", host: "::1", local: true},
		{value: "192.168.1.10", host: "192.168.1.10", local: true},
		{value: "47.239.49.135", host: "47.239.49.135", local: true},
		{value: "app.internal", host: "app.internal", local: true},
		{value: "test-https.runforyou.app", host: "test-https.runforyou.app", local: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			host, local := requestHost(test.value)
			if host != test.host || local != test.local {
				t.Fatalf("requestHost(%q) = (%q, %t), want (%q, %t)", test.value, host, local, test.host, test.local)
			}
		})
	}
}

// TestAllowCertificateRequiresHTTPEntry 验证无缓存的公网域名必须先通过 HTTP 入口访问。
func TestAllowCertificateRequiresHTTPEntry(t *testing.T) {
	service := &HTTPSEntry{cache: autocert.DirCache(t.TempDir())}
	const host = "test-https.runforyou.app"
	if err := service.allowCertificate(t.Context(), host); err == nil {
		t.Fatal("expected unapproved domain to be rejected")
	}
	service.allowed.Store(host, struct{}{})
	if err := service.allowCertificate(t.Context(), host); err != nil {
		t.Fatalf("approved domain rejected: %v", err)
	}
	service.allowed.Store("localhost", struct{}{})
	if err := service.allowCertificate(t.Context(), "localhost"); err == nil {
		t.Fatal("expected localhost to be rejected")
	}
}

// TestCertificateRequestLimiterBoundsNewAttempts 验证新证书请求受滑动时间窗口限制。
func TestCertificateRequestLimiterBoundsNewAttempts(t *testing.T) {
	limiter := &certificateRequestLimiter{}
	now := time.Now()
	for index := range certificateRequestLimit {
		if !limiter.allow("host-"+big.NewInt(int64(index)).String()+".example.com", now) {
			t.Fatalf("request %d rejected before the limit", index+1)
		}
	}
	if limiter.allow("overflow.example.com", now) {
		t.Fatal("expected request beyond the limit to be rejected")
	}
	if !limiter.allow("host-0.example.com", now.Add(certificateRequestRetryWindow-time.Second)) {
		t.Fatal("expected duplicate host inside retry window to share the attempt")
	}
	if !limiter.allow("after-window.example.com", now.Add(certificateRequestWindow+time.Second)) {
		t.Fatal("expected request after the sliding window to be accepted")
	}
}

// TestProxyHostFollowsServerListener 验证 HTTPS 反代使用可访问的监听地址。
func TestProxyHostFollowsServerListener(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0":   "127.0.0.1",
		"[::]":      "::1",
		"[::1]":     "::1",
		"localhost": "localhost",
	}
	for host, expected := range tests {
		if actual := proxyHost(host); actual != expected {
			t.Errorf("proxyHost(%q) = %q, want %q", host, actual, expected)
		}
	}
}

// TestHTTPSProxyRewritesTrustedHeaders 验证 HTTPS 反代保留租户域名并覆盖外部转发头。
func TestHTTPSProxyRewritesTrustedHeaders(t *testing.T) {
	service := NewHTTPSEntry(
		serverconfig.TLSConfig{Mode: "auto", ACMEEmail: "dev@example.com"},
		serverconfig.ServerConfig{Host: "0.0.0.0", Port: 8080},
		autocert.DirCache(t.TempDir()),
	)
	var outbound *http.Request
	service.proxy.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		outbound = request.Clone(request.Context())
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})

	request := httptest.NewRequest(http.MethodGet, "https://tenant.runforyou.app/api/health?ready=true", nil)
	request.Host = "tenant.runforyou.app"
	request.Header.Set("X-Forwarded-For", "203.0.113.10")
	request.Header.Set("X-Forwarded-Proto", "http")
	response := httptest.NewRecorder()
	service.proxy.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if outbound == nil {
		t.Fatal("expected proxy transport to receive a request")
	}
	if outbound.URL.Scheme != "http" || outbound.URL.Host != "127.0.0.1:8080" {
		t.Fatalf("proxy target = %s://%s, want http://127.0.0.1:8080", outbound.URL.Scheme, outbound.URL.Host)
	}
	if outbound.URL.Path != "/api/health" || outbound.URL.RawQuery != "ready=true" {
		t.Fatalf("proxy request URI = %q, want %q", outbound.URL.RequestURI(), "/api/health?ready=true")
	}
	if outbound.Host != "tenant.runforyou.app" {
		t.Fatalf("proxy host = %q, want tenant.runforyou.app", outbound.Host)
	}
	if protocol := outbound.Header.Get("X-Forwarded-Proto"); protocol != "https" {
		t.Fatalf("X-Forwarded-Proto = %q, want https", protocol)
	}
	if forwardedFor := outbound.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		t.Fatalf("X-Forwarded-For = %q, want empty", forwardedFor)
	}
}

// TestAllowCertificateRestoresCachedDomain 验证重启后可以从持久化证书恢复域名许可。
func TestAllowCertificateRestoresCachedDomain(t *testing.T) {
	const host = "cached.runforyou.app"
	cache := autocert.DirCache(t.TempDir())
	if err := cache.Put(t.Context(), host, cachedCertificate(t, host, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("cache certificate: %v", err)
	}
	service := &HTTPSEntry{cache: cache}
	if err := service.allowCertificate(t.Context(), host); err != nil {
		t.Fatalf("cached domain rejected: %v", err)
	}
	if _, ok := service.allowed.Load(host); !ok {
		t.Fatal("expected cached domain to be restored in memory")
	}
	if err := service.allowCertificate(t.Context(), "other.runforyou.app"); err == nil {
		t.Fatal("expected domain without matching certificate to be rejected")
	}
}

// TestAllowCertificateRejectsExpiredCachedDomain 验证域名缓存到期后重新执行证书资格校验。
func TestAllowCertificateRejectsExpiredCachedDomain(t *testing.T) {
	const host = "expired.runforyou.app"
	cache := autocert.DirCache(t.TempDir())
	data := cachedCertificate(t, host, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if err := cache.Put(t.Context(), host, data); err != nil {
		t.Fatalf("cache expired certificate: %v", err)
	}
	service := &HTTPSEntry{cache: cache}
	if err := service.allowCertificate(t.Context(), host); err == nil {
		t.Fatal("expected expired cached domain without HTTP entry to be rejected")
	}
	if _, ok := service.certified.Load(host); ok {
		t.Fatal("expected expired cached domain not to be marked as certified")
	}
}

// TestAllowCertificateAllowsPublicHost 验证部署地址的域名可以从 HTTPS 直接触发签发。
func TestAllowCertificateAllowsPublicHost(t *testing.T) {
	const host = "app.example.com"
	service := NewHTTPSEntry(
		serverconfig.TLSConfig{Mode: "auto", ACMEEmail: "dev@example.com"},
		serverconfig.ServerConfig{PublicURL: "https://App.Example.Com", Host: "0.0.0.0", Port: 8080},
		autocert.DirCache(t.TempDir()),
	)
	if err := service.allowCertificate(t.Context(), host); err != nil {
		t.Fatalf("public host rejected: %v", err)
	}
	if _, ok := service.allowed.Load(host); !ok {
		t.Fatal("expected public host to be remembered in memory")
	}
	if err := service.allowCertificate(t.Context(), "unknown.runforyou.app"); err == nil {
		t.Fatal("expected unknown domain without HTTP entry to be rejected")
	}
}

// cachedCertificate 创建符合 autocert 缓存格式的测试证书。
func cachedCertificate(t *testing.T, host string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	var data bytes.Buffer
	if err := pem.Encode(&data, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("encode private key: %v", err)
	}
	if err := pem.Encode(&data, &pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}); err != nil {
		t.Fatalf("encode certificate: %v", err)
	}
	return data.Bytes()
}
