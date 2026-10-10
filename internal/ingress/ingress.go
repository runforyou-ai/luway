//go:build server

// Package ingress 实现服务端的流量入口：在服务端口提供 HTTP，配置了 HTTPS 端口时以部署地址的证书在 HTTPS 端口提供同一套处理；HTTP 端口应答证书签发质询，并把访问部署地址域名的 HTTP 请求跳转到 HTTPS。
package ingress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	acmeintegration "github.com/runforyou-ai/luway/internal/integration/acme"
)

const (
	// requestTimeout 是读取请求与写出响应的时限，事件流按单次写出各自延长写截止时间。
	requestTimeout = 30 * time.Second
	// idleTimeout 是保持空闲连接的时限。
	idleTimeout = 120 * time.Second
	// shutdownTimeout 是停止时等待进行中请求结束的时限。
	shutdownTimeout = 30 * time.Second
)

// Deployment 提供入口使用的部署地址与证书。
type Deployment interface {
	// PublicURL 返回当前的部署地址。
	PublicURL() string
	// TLSCertificate 返回部署地址的证书，没有可用证书时为空。
	TLSCertificate() *tls.Certificate
}

// Challenges 按令牌读取证书签发质询的应答。
type Challenges interface {
	KeyAuthorization(ctx context.Context, token string) (keyAuthorization string, found bool, err error)
}

// Entry 是本服务器的 HTTP 与 HTTPS 入口。
type Entry struct {
	server      serverconfig.ServerConfig
	deployment  Deployment
	challenges  Challenges
	httpServer  *http.Server
	httpsServer *http.Server
}

// New 创建以 handler 处理请求的入口：服务端口提供 HTTP，配置了 HTTPS 端口时同时提供 HTTPS。
func New(server serverconfig.ServerConfig, deployment Deployment, challenges Challenges, handler http.Handler) *Entry {
	entry := &Entry{server: server, deployment: deployment, challenges: challenges}
	host := strings.Trim(server.Host, "[]")
	entry.httpServer = newServer(net.JoinHostPort(host, strconv.Itoa(server.Port)), entry.plainHTTP(handler))
	if server.HTTPSPort != 0 {
		entry.httpsServer = newServer(net.JoinHostPort(host, strconv.Itoa(server.HTTPSPort)), handler)
		entry.httpsServer.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				if certificate := deployment.TLSCertificate(); certificate != nil {
					return certificate, nil
				}
				return nil, errors.New("deployment has no certificate")
			},
		}
	}
	return entry
}

// newServer 创建按统一时限处理请求的 HTTP 服务。
func newServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         address,
		Handler:      handler,
		ReadTimeout:  requestTimeout,
		WriteTimeout: requestTimeout,
		IdleTimeout:  idleTimeout,
	}
}

// Start 开始监听服务端口与 HTTPS 端口，任一端口无法监听时返回错误；监听意外结束时记录错误。
func (e *Entry) Start(ctx context.Context) error {
	servers := []*http.Server{e.httpServer}
	if e.httpsServer != nil {
		servers = append(servers, e.httpsServer)
	}
	listeners := make([]net.Listener, 0, len(servers))
	for _, server := range servers {
		listener, err := net.Listen("tcp", server.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return fmt.Errorf("listen on %s: %w", server.Addr, err)
		}
		listeners = append(listeners, listener)
	}
	for index, server := range servers {
		listener := listeners[index]
		secure := server == e.httpsServer
		go func() {
			var err error
			if secure {
				err = server.ServeTLS(listener, "", "")
			} else {
				err = server.Serve(listener)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.ErrorContext(ctx, "服务端监听意外结束", "address", server.Addr, "error", err)
			}
		}()
	}
	slog.InfoContext(ctx, "服务端已开始监听", "address", e.httpServer.Addr, "https_address", httpsAddress(e.httpsServer))
	return nil
}

// Stop 停止监听并在时限内等待进行中的请求结束。
func (e *Entry) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := e.httpServer.Shutdown(ctx)
	if e.httpsServer != nil {
		err = errors.Join(err, e.httpsServer.Shutdown(ctx))
	}
	return err
}

// httpsAddress 返回 HTTPS 监听地址，未提供 HTTPS 时为空。
func httpsAddress(server *http.Server) string {
	if server == nil {
		return ""
	}
	return server.Addr
}

// plainHTTP 包装服务端口的处理：应答证书签发质询；本服务器提供 HTTPS 且部署地址为 HTTPS 时，把访问部署地址域名的请求跳转到部署地址的相同路径，其余请求交给 next。
func (e *Entry) plainHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if token, ok := strings.CutPrefix(request.URL.Path, acmeintegration.ChallengePath); ok {
			keyAuthorization, found, err := e.challenges.KeyAuthorization(request.Context(), token)
			if err != nil {
				slog.WarnContext(request.Context(), "读取证书签发质询失败", "error", err)
				http.Error(writer, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			if !found {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte(keyAuthorization))
			return
		}
		// 按 IP 或本机地址访问的请求不跳转。
		if e.server.HTTPSPort != 0 {
			target, err := url.Parse(e.deployment.PublicURL())
			host, _, splitErr := net.SplitHostPort(request.Host)
			if splitErr != nil {
				host = request.Host
			}
			if err == nil && target.Scheme == "https" && strings.EqualFold(host, target.Hostname()) {
				http.Redirect(writer, request, target.Scheme+"://"+target.Host+request.URL.RequestURI(), http.StatusTemporaryRedirect)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}
