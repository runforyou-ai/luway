//go:build server

// Package ingress 实现企业服务端的公网流量入口。
package ingress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const (
	httpAddress  = ":80"
	httpsAddress = ":443"
	modeAuto     = tlsMode("auto")
	modeExternal = tlsMode("external")
	modeOff      = tlsMode("off")
	// maxAllowedHostEntries 定义已确认公网域名缓存的容量上限。
	maxAllowedHostEntries = 1024
	// certificateRequestLimit 为 autocert 的双挑战、双密钥签发路径预留 Let's Encrypt 订单额度。
	certificateRequestLimit       = 40
	certificateRequestWindow      = 3 * time.Hour
	certificateRequestRetryWindow = time.Minute
)

type tlsMode string

// certificateRequestLimiter 限制可能创建 ACME 订单的新证书请求。
type certificateRequestLimiter struct {
	mutex    sync.Mutex
	attempts []time.Time
	hosts    map[string]time.Time
}

// HTTPSEntry 按 TLS 模式代理 Wails server。
type HTTPSEntry struct {
	mode         tlsMode
	cache        autocert.Cache
	publicHost   string
	manager      *autocert.Manager
	proxy        *httputil.ReverseProxy
	httpServer   *http.Server
	httpsServer  *http.Server
	allowed      sync.Map
	certified    sync.Map
	allowedMutex sync.Mutex
	allowedCount int
	requests     certificateRequestLimiter
}

// NewHTTPSEntry 根据 TLS 配置和共享缓存创建 HTTPS 入口。
func NewHTTPSEntry(
	config serverconfig.TLSConfig,
	backend serverconfig.ServerConfig,
	cache autocert.Cache,
) *HTTPSEntry {
	mode := tlsMode(config.Mode)
	publicHost := ""
	if parsed, err := url.Parse(backend.PublicURL); err == nil {
		publicHost, _ = requestHost(parsed.Host)
	}
	service := &HTTPSEntry{mode: mode, publicHost: publicHost}
	if mode != modeAuto {
		return service
	}
	if config.ACMEEmail == "" {
		slog.Warn("自动 HTTPS 未配置 ACME 联系邮箱")
	}

	backendURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(proxyHost(backend.Host), strconv.Itoa(backend.Port))}
	service.proxy = &httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(backendURL)
		request.Out.Host = request.In.Host
		protocol := "http"
		if request.In.TLS != nil {
			protocol = "https"
		}
		request.Out.Header.Set("X-Forwarded-Proto", protocol)
	}}
	service.proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		slog.Warn("转发 HTTPS 请求失败", "host", request.Host, "path", request.URL.Path, "error", err)
		http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
	}

	service.cache = cache
	service.manager = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      service.cache,
		HostPolicy: service.allowCertificate,
		Email:      config.ACMEEmail,
	}
	tlsConfig := service.manager.TLSConfig()
	tlsConfig.GetCertificate = service.getCertificate
	service.httpServer = &http.Server{
		Addr:              httpAddress,
		Handler:           service.manager.HTTPHandler(http.HandlerFunc(service.serveHTTP)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	service.httpsServer = &http.Server{
		Addr:              httpsAddress,
		Handler:           service.proxy,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return service
}

// getCertificate 在 autocert 发起新证书流程前施加全局速率限制。
func (s *HTTPSEntry) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
		return s.manager.GetCertificate(hello)
	}
	host, local := requestHost(hello.ServerName)
	if host == "" || local {
		return nil, fmt.Errorf("host is not a public domain")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.allowCertificate(ctx, host); err != nil {
		return nil, err
	}
	if _, ok := s.certified.Load(host); !ok && s.cachedCertificateMatches(ctx, host) {
		s.markCertifiedHost(host)
	}
	if _, ok := s.certified.Load(host); !ok && !s.requests.allow(host, time.Now()) {
		slog.Warn(
			"自动 HTTPS 证书请求超过速率限制",
			"domain", host,
			"limit", certificateRequestLimit,
			"window", certificateRequestWindow,
		)
		return nil, fmt.Errorf("automatic certificate request rate limit exceeded")
	}
	certificate, err := s.manager.GetCertificate(hello)
	if err == nil {
		s.markCertifiedHost(host)
	}
	return certificate, err
}

// allow 对同一域名一分钟内的并发或失败重试只计一次。
func (l *certificateRequestLimiter) allow(host string, now time.Time) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if last, ok := l.hosts[host]; ok && now.Before(last.Add(certificateRequestRetryWindow)) {
		return true
	}

	cutoff := now.Add(-certificateRequestWindow)
	firstActive := 0
	for firstActive < len(l.attempts) && !l.attempts[firstActive].After(cutoff) {
		firstActive++
	}
	if firstActive > 0 {
		l.attempts = append([]time.Time(nil), l.attempts[firstActive:]...)
	}
	for existingHost, last := range l.hosts {
		if !last.Add(certificateRequestRetryWindow).After(now) {
			delete(l.hosts, existingHost)
		}
	}
	if len(l.attempts) >= certificateRequestLimit {
		return false
	}
	if l.hosts == nil {
		l.hosts = make(map[string]time.Time)
	}
	l.hosts[host] = now
	l.attempts = append(l.attempts, now)
	return true
}

// proxyHost 返回 HTTPS 入口访问服务监听器使用的地址。
func proxyHost(host string) string {
	host = strings.Trim(host, "[]")
	switch host {
	case "0.0.0.0":
		return "127.0.0.1"
	case "::":
		return "::1"
	default:
		return host
	}
}

// Start 启动 HTTPS 入口。
func (s *HTTPSEntry) Start(ctx context.Context) error {
	if s.mode == modeExternal {
		slog.Info("TLS 由外部入口终止")
		return nil
	}
	if s.mode == modeOff {
		slog.Info("TLS 入口已关闭")
		return nil
	}
	httpListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP on %s: %w", s.httpServer.Addr, err)
	}
	httpsListener, err := net.Listen("tcp", s.httpsServer.Addr)
	if err != nil {
		httpListener.Close()
		return fmt.Errorf("listen HTTPS on %s: %w", s.httpsServer.Addr, err)
	}

	go s.serve("HTTP", func() error { return s.httpServer.Serve(httpListener) })
	go s.serve("HTTPS", func() error { return s.httpsServer.ServeTLS(httpsListener, "", "") })
	go func() {
		<-ctx.Done()
		if err := s.Shutdown(); err != nil {
			slog.Warn("关闭自动 HTTPS 服务失败", "error", err)
		}
	}()
	slog.Info("自动 HTTPS 入口已启动", "http", httpAddress, "https", httpsAddress, "certificate_cache", "postgresql")
	return nil
}

// Shutdown 关闭 HTTP 和 HTTPS 监听器。
func (s *HTTPSEntry) Shutdown() error {
	if s.mode != modeAuto {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := s.httpServer.Shutdown(ctx)
	httpsErr := s.httpsServer.Shutdown(ctx)
	return errors.Join(httpErr, httpsErr)
}

// serve 运行一个入口并记录无法恢复的监听错误。
func (s *HTTPSEntry) serve(protocol string, run func() error) {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("自动 HTTPS 入口停止", "protocol", protocol, "error", err)
	}
}

// serveHTTP 为本地地址保留 HTTP，并为公网域名启用 HTTPS。
func (s *HTTPSEntry) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	host, local := requestHost(request.Host)
	if local {
		s.proxy.ServeHTTP(writer, request)
		return
	}
	if host == "" {
		http.NotFound(writer, request)
		return
	}
	s.rememberAllowedHost(host)
	// 把公网 HTTP 请求跳转到相同域名和路径的 HTTPS 地址。
	target := url.URL{Scheme: "https", Host: host, Path: request.URL.Path, RawQuery: request.URL.RawQuery}
	http.Redirect(writer, request, target.String(), http.StatusTemporaryRedirect)
}

// rememberAllowedHost 缓存已确认的公网域名，达到容量上限时清空并重建缓存。
func (s *HTTPSEntry) rememberAllowedHost(host string) {
	s.allowedMutex.Lock()
	defer s.allowedMutex.Unlock()
	if _, ok := s.allowed.Load(host); ok {
		return
	}
	if s.allowedCount >= maxAllowedHostEntries {
		s.allowed.Clear()
		s.certified.Clear()
		s.allowedCount = 0
	}
	s.allowed.Store(host, struct{}{})
	s.allowedCount++
}

// markCertifiedHost 缓存已签发域名，并与已确认域名共享容量上限。
func (s *HTTPSEntry) markCertifiedHost(host string) {
	s.rememberAllowedHost(host)
	s.certified.Store(host, struct{}{})
}

// allowCertificate 只允许部署地址、通过 HTTP 入口或已有证书的公网域名。
func (s *HTTPSEntry) allowCertificate(ctx context.Context, host string) error {
	host, local := requestHost(host)
	if host == "" || local {
		return fmt.Errorf("host is not a public domain")
	}
	if _, ok := s.allowed.Load(host); ok {
		return nil
	}
	if s.cachedCertificateMatches(ctx, host) {
		s.markCertifiedHost(host)
		slog.Info("已从证书缓存恢复 HTTPS 域名", "domain", host)
		return nil
	}
	if host == s.publicHost {
		s.rememberAllowedHost(host)
		slog.Info("已按部署地址允许 HTTPS 域名", "domain", host)
		return nil
	}
	return fmt.Errorf("host has not entered through HTTP")
}

// cachedCertificateMatches 判断持久化缓存中是否存在属于该域名的证书。
func (s *HTTPSEntry) cachedCertificateMatches(ctx context.Context, host string) bool {
	now := time.Now()
	for _, key := range []string{host, host + "+rsa"} {
		data, err := s.cache.Get(ctx, key)
		if errors.Is(err, autocert.ErrCacheMiss) {
			continue
		}
		if err != nil {
			slog.Warn("读取证书缓存失败", "domain", host, "cache_key", key, "error", err)
			continue
		}
		certificate, err := tls.X509KeyPair(data, data)
		if err != nil {
			slog.Warn("证书缓存内容无效", "domain", host, "cache_key", key, "error", err)
			continue
		}
		if len(certificate.Certificate) == 0 {
			slog.Warn("证书缓存缺少证书链", "domain", host, "cache_key", key)
			continue
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			slog.Warn("解析缓存证书失败", "domain", host, "cache_key", key, "error", err)
			continue
		}
		if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			slog.Warn("缓存证书不在有效期内", "domain", host, "cache_key", key)
			continue
		}
		if err := leaf.VerifyHostname(host); err == nil {
			return true
		}
		slog.Warn("缓存证书域名不匹配", "domain", host, "cache_key", key)
	}
	return false
}

// requestHost 规范化请求域名并判断是否应当保留 HTTP。
func requestHost(value string) (string, bool) {
	host := normalizeHostname(value)
	if host == "" {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, true
	}
	local := host == "localhost" ||
		!strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".home.arpa")
	return host, local
}

// normalizeHostname 去除端口、末尾点和大小写差异，得到 TLS 使用的主机名。
func normalizeHostname(value string) string {
	hostname := strings.TrimSpace(value)
	if parsed, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = parsed
	}
	hostname = strings.Trim(hostname, "[]")
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
}
