// Package clientrelease 读取服务器客户端目录中与服务端同版本的客户端安装包与桌面端更新包，并在 /clients/ 下提供下载与更新清单。
package clientrelease

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
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

// UpdatePath 是桌面端更新清单相对部署地址的访问路径。
const UpdatePath = PathPrefix + "update"

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
	// Updates 是目录中的桌面端更新包。
	Updates []Update `json:"updates"`
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

// Update 是一个桌面端更新包，内含替换已安装程序的应用包或可执行文件。
type Update struct {
	// OS 是更新包的操作系统：windows 或 darwin。
	OS string `json:"os"`
	// Arch 是更新包的处理器架构：amd64、arm64 或 universal。
	Arch string `json:"arch"`
	// Name 是更新包在客户端目录中的文件名。
	Name string `json:"name"`
	// Size 是更新包的字节数。
	Size int64 `json:"size"`
	// SHA256 是更新包内容的十六进制 SHA-256 摘要。
	SHA256 string `json:"sha256"`
	// Signature 是对更新包 SHA-512 摘要的 Ed25519ph 签名，取标准 Base64 编码。
	Signature string `json:"signature"`
}

// Catalog 是服务器提供下载的客户端安装包与桌面端更新包。
type Catalog struct {
	directory string
	version   string
	names     map[string]struct{}
	ordered   []File
	updates   []Update
}

// Load 读取客户端目录的索引，校验安装包与更新包的大小与摘要，并用 updateKey 校验更新包签名；目录没有索引或索引版本与服务端版本不同时不提供客户端。
func Load(directory, serverVersion string, updateKey ed25519.PublicKey) (*Catalog, error) {
	catalog := &Catalog{directory: directory, names: map[string]struct{}{}}
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
		if _, err := catalog.add(file.Name, file.Size, file.SHA256); err != nil {
			return nil, fmt.Errorf("校验客户端安装包 %s: %w", file.Name, err)
		}
		catalog.ordered = append(catalog.ordered, file)
	}
	for _, update := range index.Updates {
		digest, err := catalog.add(update.Name, update.Size, update.SHA256)
		if err != nil {
			return nil, fmt.Errorf("校验桌面端更新包 %s: %w", update.Name, err)
		}
		signature, err := base64.StdEncoding.DecodeString(update.Signature)
		if err != nil || ed25519.VerifyWithOptions(updateKey, digest, signature, &ed25519.Options{Hash: crypto.SHA512}) != nil {
			return nil, fmt.Errorf("校验桌面端更新包 %s: 签名无效", update.Name)
		}
		catalog.updates = append(catalog.updates, update)
	}
	catalog.version = index.Version
	return catalog, nil
}

// add 登记目录下的一个文件，校验文件名、大小与 SHA-256 摘要并返回其 SHA-512 摘要。
func (c *Catalog) add(name string, size int64, sha256Digest string) ([]byte, error) {
	// 文件名只能是客户端目录下的单层文件。
	if name == "" || name == IndexName || filepath.Base(name) != name || strings.HasPrefix(name, ".") || PathPrefix+name == UpdatePath {
		return nil, fmt.Errorf("文件名无效: %q", name)
	}
	if _, ok := c.names[name]; ok {
		return nil, fmt.Errorf("文件名重复")
	}
	content, err := os.Open(filepath.Join(c.directory, name))
	if err != nil {
		return nil, err
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return nil, fmt.Errorf("大小与索引记录不一致")
	}
	short, long := sha256.New(), sha512.New()
	if _, err := io.Copy(io.MultiWriter(short, long), content); err != nil {
		return nil, err
	}
	if hex.EncodeToString(short.Sum(nil)) != sha256Digest {
		return nil, fmt.Errorf("SHA-256 摘要与索引记录不一致")
	}
	c.names[name] = struct{}{}
	return long.Sum(nil), nil
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

// ServeHTTP 在 UpdatePath 输出桌面端更新清单，其余路径按文件名输出索引中登记的文件，未登记的路径返回 404。
func (c *Catalog) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path == UpdatePath {
		c.serveUpdate(writer, request)
		return
	}
	name := strings.TrimPrefix(request.URL.Path, PathPrefix)
	if _, ok := c.names[name]; !ok {
		http.NotFound(writer, request)
		return
	}
	content, err := os.Open(filepath.Join(c.directory, name))
	if err != nil {
		slog.Error("打开客户端文件失败", "name", name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		slog.Error("读取客户端文件失败", "name", name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// 文件名带版本号，同名文件内容不变。
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, name, info.ModTime(), content)
}

// updateManifest 是 Wails 更新器 endpoint 提供方读取的更新清单。
type updateManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Version       string           `json:"version"`
	Artifacts     []updateArtifact `json:"artifacts"`
}

// updateArtifact 是更新清单中的一个更新包，下载地址相对清单地址解析。
type updateArtifact struct {
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

// serveUpdate 按查询参数 platform、arch 输出只含对应更新包的更新清单，没有对应更新包时返回 204；是否升级由客户端比较版本决定。
func (c *Catalog) serveUpdate(writer http.ResponseWriter, request *http.Request) {
	platform, arch := request.URL.Query().Get("platform"), request.URL.Query().Get("arch")
	writer.Header().Set("Cache-Control", "no-store")
	for _, update := range c.updates {
		if update.OS != platform || (update.Arch != arch && update.Arch != "universal") {
			continue
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(updateManifest{
			SchemaVersion: 1,
			Version:       c.version,
			Artifacts: []updateArtifact{{
				URL: update.Name, Filename: update.Name, Size: update.Size, Platform: platform, Arch: arch,
				SignatureAlgo: "ed25519ph", Signature: update.Signature,
			}},
		})
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
