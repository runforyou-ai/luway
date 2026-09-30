package webasset

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// contentTypes 是按扩展名固定的响应类型，未列出的扩展名按内容嗅探。
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".ico":   "image/x-icon",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".mjs":   "text/javascript; charset=utf-8",
	".png":   "image/png",
	".svg":   "image/svg+xml",
	".ttf":   "font/ttf",
	".wasm":  "application/wasm",
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// FileServer 提供启动时整体载入并预压缩的静态目录。
type FileServer struct {
	assets       map[string]Asset
	immutableDir string
}

// NewFileServer 载入目录下全部文件；immutableDir 下的文件名含内容哈希，使用长期缓存，其余文件按 ETag 校验。
func NewFileServer(files fs.FS, immutableDir string) (*FileServer, error) {
	assets := map[string]Asset{}
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		contentType, ok := contentTypes[path.Ext(name)]
		if !ok {
			contentType = http.DetectContentType(raw)
		}
		assets[name] = New(contentType, raw)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load static assets: %w", err)
	}
	if _, ok := assets["index.html"]; !ok {
		return nil, fmt.Errorf("load static assets: index.html not found")
	}
	return &FileServer{assets: assets, immutableDir: strings.TrimSuffix(immutableDir, "/") + "/"}, nil
}

// Replace 用指定内容替换目录中的文件。
func (s *FileServer) Replace(name string, raw []byte) {
	contentType, ok := contentTypes[path.Ext(name)]
	if !ok {
		contentType = http.DetectContentType(raw)
	}
	s.assets[name] = New(contentType, raw)
}

// ServeHTTP 按规范化后的请求路径返回文件，根路径返回 index.html；错误响应不缓存。
func (s *FileServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writer.Header().Set("Cache-Control", "no-store")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	asset, ok := s.assets[name]
	if !ok {
		writer.Header().Set("Cache-Control", "no-store")
		http.NotFound(writer, request)
		return
	}
	cacheControl := RevalidateCache
	if strings.HasPrefix(name, s.immutableDir) {
		cacheControl = ImmutableCache
	}
	asset.Serve(writer, request, cacheControl)
}
