// Package clientrelease 读取服务器客户端目录中与服务端同版本的客户端安装包与更新包，在 /clients/ 下提供下载，并为桌面端输出更新清单。
package clientrelease

import (
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
	"path"
	"path/filepath"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"golang.org/x/mod/semver"
)

// IndexName 是客户端目录中描述安装包与更新包的索引文件名。
const IndexName = "clients.json"

// PathPrefix 是客户端文件相对部署地址的下载路径前缀。
const PathPrefix = "/clients/"

// 客户端的操作系统，取值与 Go 的 GOOS 一致。
const (
	OSWindows = "windows"
	OSDarwin  = "darwin"
	OSLinux   = "linux"
)

// ArchUniversal 是同时支持 amd64 与 arm64 的 macOS 通用架构。
const ArchUniversal = "universal"

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
	// Version 是客户端版本。
	Version string `json:"version"`
	// Installers 是首次安装使用的安装包，按展示顺序排列。
	Installers []Installer `json:"installers"`
	// Updates 是桌面端应用内更新替换应用使用的更新包，每个平台与架构至多一个。
	Updates []Update `json:"updates"`
}

// File 是客户端目录中的一个文件。
type File struct {
	// Name 是文件在客户端目录中的文件名。
	Name string `json:"name"`
	// Size 是文件的字节数。
	Size int64 `json:"size"`
	// SHA512 是文件内容的十六进制 SHA-512 摘要。
	SHA512 string `json:"sha512"`
}

// Installer 是一个客户端安装包。
type Installer struct {
	// OS 是安装包的操作系统：windows、darwin 或 linux。
	OS string `json:"os"`
	// Arch 是安装包的处理器架构：amd64、arm64 或 universal。
	Arch string `json:"arch"`
	// Format 是安装包格式：exe、dmg、appimage、deb、rpm 或 pacman。
	Format string `json:"format"`
	File
}

// Update 是一个桌面端更新包：macOS 为根目录只含应用包的 zip，Windows 为应用可执行文件。
type Update struct {
	// OS 是更新包的操作系统：windows 或 darwin。
	OS string `json:"os"`
	// Arch 是更新包的处理器架构：amd64、arm64 或 universal。
	Arch string `json:"arch"`
	File
	// Signature 是对文件 SHA-512 摘要的 Ed25519ph 签名，base64 编码。
	Signature string `json:"signature"`
}

// Catalog 是服务器提供的客户端安装包与更新包。
type Catalog struct {
	directory  string
	version    string
	installers []Installer
	updates    []Update
	files      map[string]File
}

// Load 读取客户端目录的索引并校验文件的大小与摘要；目录没有索引或索引版本与服务端版本不同时不提供客户端。
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
	files := make([]File, 0, len(index.Installers)+len(index.Updates))
	for _, installer := range index.Installers {
		files = append(files, installer.File)
	}
	platforms := map[string]bool{}
	for _, update := range index.Updates {
		if platforms[update.OS+"/"+update.Arch] {
			return nil, fmt.Errorf("客户端索引中 %s/%s 的更新包重复", update.OS, update.Arch)
		}
		platforms[update.OS+"/"+update.Arch] = true
		files = append(files, update.File)
	}
	for _, file := range files {
		// 文件名只能是客户端目录下的单层文件，且不与索引和更新清单路径重名。
		if file.Name == "" || file.Name == IndexName || file.Name == path.Base(domain.ClientUpdatePath) || filepath.Base(file.Name) != file.Name || strings.HasPrefix(file.Name, ".") {
			return nil, fmt.Errorf("客户端索引中的文件名无效: %q", file.Name)
		}
		if _, ok := catalog.files[file.Name]; ok {
			return nil, fmt.Errorf("客户端索引中的文件名重复: %s", file.Name)
		}
		if err := verify(filepath.Join(directory, file.Name), file); err != nil {
			return nil, fmt.Errorf("校验客户端文件 %s: %w", file.Name, err)
		}
		catalog.files[file.Name] = file
	}
	catalog.version = index.Version
	catalog.installers = index.Installers
	catalog.updates = index.Updates
	return catalog, nil
}

// verify 校验文件是普通文件，且大小与 SHA-512 摘要与索引记录一致。
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
	hash := sha512.New()
	if _, err := io.Copy(hash, content); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.SHA512 {
		return fmt.Errorf("SHA-512 摘要与索引记录不一致")
	}
	return nil
}

// Version 返回服务器提供的客户端版本，没有任何客户端文件时为空。
func (c *Catalog) Version() string {
	if len(c.files) == 0 {
		return ""
	}
	return c.version
}

// Installers 返回提供下载的安装包，按索引顺序排列。
func (c *Catalog) Installers() []Installer {
	return c.installers
}

// URL 返回文件相对部署地址的下载地址，查询参数 v 取内容摘要前缀，同名文件内容变化时地址随之变化。
func URL(file File) string {
	return PathPrefix + file.Name + "?v=" + file.SHA512[:16]
}

// ServeHTTP 在更新清单路径输出桌面端更新清单，其余路径按文件名输出索引中登记的文件。
func (c *Catalog) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path == domain.ClientUpdatePath {
		c.serveManifest(writer, request)
		return
	}
	file, ok := c.files[strings.TrimPrefix(request.URL.Path, PathPrefix)]
	if !ok {
		http.NotFound(writer, request)
		return
	}
	content, err := os.Open(filepath.Join(c.directory, file.Name))
	if err != nil {
		slog.Error("打开客户端文件失败", "name", file.Name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		slog.Error("读取客户端文件失败", "name", file.Name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// 下载地址带内容摘要，同一地址的内容不变。
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, file.Name, info.ModTime(), content)
}

// manifest 是 Wails 更新清单协议的清单文档。
type manifest struct {
	SchemaVersion int                `json:"schemaVersion"`
	Version       string             `json:"version"`
	Artifacts     []manifestArtifact `json:"artifacts"`
}

// manifestArtifact 是更新清单中当前平台的更新包。
type manifestArtifact struct {
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

// serveManifest 按查询参数 platform、arch 与 version 输出只含该平台更新包的更新清单；客户端版本不低于服务器提供的版本时返回 204，没有该平台更新包时返回 404。
func (c *Catalog) serveManifest(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-cache")
	query := request.URL.Query()
	current := "v" + query.Get("version")
	if !semver.IsValid(current) {
		http.Error(writer, "version 必须是语义化版本", http.StatusBadRequest)
		return
	}
	if c.Version() == "" || semver.Compare("v"+c.version, current) <= 0 {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	platform, arch := query.Get("platform"), query.Get("arch")
	for _, update := range c.updates {
		if update.OS != platform || (update.Arch != arch && update.Arch != ArchUniversal) {
			continue
		}
		// 加载时已核对摘要等于文件内容的十六进制 SHA-512。
		digest, _ := hex.DecodeString(update.SHA512)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(manifest{SchemaVersion: 1, Version: c.version, Artifacts: []manifestArtifact{{
			URL: URL(update.File), Filename: update.Name, Size: update.Size, Platform: platform, Arch: arch,
			DigestAlgo: "sha512", Digest: base64.StdEncoding.EncodeToString(digest),
			SignatureAlgo: "ed25519ph", Signature: update.Signature,
		}}})
		return
	}
	http.NotFound(writer, request)
}
