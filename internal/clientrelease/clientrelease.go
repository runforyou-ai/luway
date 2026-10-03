// Package clientrelease 读取服务器客户端目录中与服务端同版本的客户端安装包，并在 /clients/ 下提供下载。
package clientrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// IndexName 是客户端目录中描述安装包的索引文件名。
const IndexName = "clients.json"

// PathPrefix 是安装包相对部署地址的下载路径前缀。
const PathPrefix = "/clients/"

// 安装包的操作系统。
const (
	OSWindows = "windows"
	OSDarwin  = "darwin"
	OSLinux   = "linux"
)

// 安装包格式。
const (
	FormatEXE      = "exe"
	FormatDMG      = "dmg"
	FormatAppImage = "appimage"
	FormatDeb      = "deb"
	FormatRPM      = "rpm"
	FormatPacman   = "pacman"
)

// Index 是客户端目录的索引文件内容。
type Index struct {
	// Version 是安装包的客户端版本。
	Version string `json:"version"`
	// Files 是目录中的安装包，按展示顺序排列。
	Files []File `json:"files"`
}

// File 是一个客户端安装包。
type File struct {
	// OS 是安装包的操作系统：windows、darwin 或 linux。
	OS string `json:"os"`
	// Arch 是安装包的处理器架构：amd64、arm64 或 universal。
	Arch string `json:"arch"`
	// Format 是安装包格式：exe、dmg、appimage、deb、rpm 或 pacman。
	Format string `json:"format"`
	// Name 是安装包在客户端目录中的文件名。
	Name string `json:"name"`
	// Size 是安装包的字节数。
	Size int64 `json:"size"`
	// SHA256 是安装包内容的十六进制 SHA-256 摘要。
	SHA256 string `json:"sha256"`
}

// Catalog 是服务器提供下载的客户端安装包。
type Catalog struct {
	directory string
	version   string
	files     map[string]File
	ordered   []File
}

// Load 读取客户端目录的索引并校验安装包的大小与摘要；目录没有索引或索引版本与服务端版本不同时不提供安装包。
func Load(directory, serverVersion string) (*Catalog, error) {
	catalog := &Catalog{directory: directory, files: map[string]File{}}
	data, err := os.ReadFile(filepath.Join(directory, IndexName))
	if errors.Is(err, fs.ErrNotExist) {
		return catalog, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取客户端索引: %w", err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("解析客户端索引: %w", err)
	}
	if index.Version != serverVersion {
		slog.Warn("客户端目录版本与服务端版本不同，不提供客户端下载", "directory", directory, "clients_version", index.Version, "server_version", serverVersion)
		return catalog, nil
	}
	for _, file := range index.Files {
		// 文件名只能是客户端目录下的单层文件。
		if file.Name == "" || file.Name == IndexName || filepath.Base(file.Name) != file.Name || strings.HasPrefix(file.Name, ".") {
			return nil, fmt.Errorf("客户端索引中的文件名无效: %q", file.Name)
		}
		if _, ok := catalog.files[file.Name]; ok {
			return nil, fmt.Errorf("客户端索引中的文件名重复: %s", file.Name)
		}
		if err := verify(filepath.Join(directory, file.Name), file); err != nil {
			return nil, fmt.Errorf("校验客户端安装包 %s: %w", file.Name, err)
		}
		catalog.files[file.Name] = file
		catalog.ordered = append(catalog.ordered, file)
	}
	catalog.version = index.Version
	return catalog, nil
}

// verify 校验文件是普通文件，且大小与 SHA-256 摘要与索引记录一致。
func verify(path string, file File) error {
	content, err := os.Open(path)
	if err != nil {
		return err
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != file.Size {
		return fmt.Errorf("大小与索引记录不一致")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, content); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("SHA-256 摘要与索引记录不一致")
	}
	return nil
}

// Version 返回提供下载的客户端版本，没有安装包时为空。
func (c *Catalog) Version() string {
	if len(c.ordered) == 0 {
		return ""
	}
	return c.version
}

// Files 返回提供下载的安装包，按索引顺序排列。
func (c *Catalog) Files() []File {
	return c.ordered
}

// URL 返回安装包相对部署地址的下载路径。
func URL(file File) string {
	return PathPrefix + file.Name
}

// ServeHTTP 按文件名输出索引中登记的安装包，其余路径返回 404。
func (c *Catalog) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	file, ok := c.files[strings.TrimPrefix(request.URL.Path, PathPrefix)]
	if !ok {
		http.NotFound(writer, request)
		return
	}
	content, err := os.Open(filepath.Join(c.directory, file.Name))
	if err != nil {
		slog.Error("打开客户端安装包失败", "name", file.Name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		slog.Error("读取客户端安装包失败", "name", file.Name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// 文件名带版本号，同名文件内容不变。
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, file.Name, info.ModTime(), content)
}
