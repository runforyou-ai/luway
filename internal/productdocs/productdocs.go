//go:build server

// Package productdocs 在 /docs/ 下提供随服务端构建内置的产品文档站点。
package productdocs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/internal/webasset"
)

// Prefix 是产品文档的访问路径前缀。
const Prefix = "/docs/"

// files 包含文档站点构建产物；未构建时 dist 下只有占位文件。
//
//go:embed all:dist
var files embed.FS

// Middleware 返回在 Prefix 下提供内置文档的中间件，icon 非空时替换站点图标；未内置文档时请求交给下一个处理器。
func Middleware(icon []byte) (func(http.Handler) http.Handler, error) {
	site, err := fs.Sub(files, "dist/site")
	if err != nil {
		return nil, fmt.Errorf("open product docs: %w", err)
	}
	return siteMiddleware(site, icon)
}

// siteMiddleware 返回在 Prefix 下提供指定文档站点的中间件，站点缺少 index.html 时请求交给下一个处理器。
func siteMiddleware(site fs.FS, icon []byte) (func(http.Handler) http.Handler, error) {
	if _, err := fs.Stat(site, "index.html"); errors.Is(err, fs.ErrNotExist) {
		slog.Warn("服务端未内置产品文档")
		return func(next http.Handler) http.Handler { return next }, nil
	}
	server, err := webasset.NewFileServer(site, "_astro")
	if err != nil {
		return nil, fmt.Errorf("load product docs: %w", err)
	}
	if len(icon) > 0 {
		server.Replace("favicon.png", icon)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == strings.TrimSuffix(Prefix, "/") {
				http.Redirect(writer, request, Prefix, http.StatusMovedPermanently)
				return
			}
			name, ok := strings.CutPrefix(request.URL.Path, Prefix)
			if !ok {
				next.ServeHTTP(writer, request)
				return
			}
			// 文件服务器按去掉前缀后的站点内路径查找资源。
			docsRequest := request.Clone(request.Context())
			docsRequest.URL.Path = "/" + name
			docsRequest.URL.RawPath = ""
			server.ServeHTTP(writer, docsRequest)
		})
	}, nil
}
