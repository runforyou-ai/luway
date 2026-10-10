// Package clientrelease 按服务端程序目录中的发布清单读取与服务端同版本的客户端安装包、桌面端更新包与无界面执行器，并在 /clients/ 下提供下载与更新清单。
package clientrelease

import (
	"context"
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

	"github.com/runforyou-ai/luway/internal/release"
)

// DirName 是程序目录中存放客户端安装包、更新包与执行器的子目录名。
const DirName = "clients"

// PathPrefix 是安装包相对部署地址的下载路径前缀。
const PathPrefix = "/clients/"

// UpdatePath 是桌面端更新清单相对部署地址的访问路径。
const UpdatePath = PathPrefix + "update"

// SignatureMetadataKey 是更新清单 metadata 中更新包签名的字段名。
const SignatureMetadataKey = "signature"

// UpdateStatement 返回更新包签名覆盖的内容：客户端版本与更新包的 SHA-512 摘要。
func UpdateStatement(version string, digest []byte) []byte {
	return []byte(version + "\n" + hex.EncodeToString(digest))
}

// servedUpdate 是通过签名校验的更新包及其 SHA-512 摘要。
type servedUpdate struct {
	release.Update
	digest []byte
}

// Catalog 是服务器提供下载的客户端安装包、桌面端更新包与无界面执行器。
type Catalog struct {
	directory  string
	version    string
	names      map[string]struct{}
	installers []release.Installer
	executors  []release.Executor
	updates    []servedUpdate
}

// ProgramDirectory 返回本程序所在的目录，其中存放同版本的发布清单与客户端目录；路径中的符号链接被解析。
func ProgramDirectory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate server executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("locate server executable: %w", err)
	}
	return filepath.Dir(executable), nil
}

// Load 读取程序目录 programDirectory 中的发布清单并用 updateKey 校验清单签名，再校验清单列出的、位于其 clients 子目录中的安装包、更新包与执行器的大小、摘要与更新包签名；没有发布清单或清单版本与服务端版本不同时不提供任何文件。
func Load(programDirectory, serverVersion string, updateKey ed25519.PublicKey) (*Catalog, error) {
	catalog := &Catalog{directory: filepath.Join(programDirectory, DirName), names: map[string]struct{}{}}
	manifest, err := release.ReadDir(programDirectory, updateKey)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog, nil
	}
	if err != nil {
		return nil, err
	}
	if manifest.Version != serverVersion {
		slog.WarnContext(context.Background(), "发布清单版本与服务端版本不同，不提供客户端下载", "directory", programDirectory, "release_version", manifest.Version, "server_version", serverVersion)
		return catalog, nil
	}
	for _, installer := range manifest.Installers {
		if _, err := catalog.add(installer.File); err != nil {
			return nil, fmt.Errorf("校验客户端安装包 %s: %w", installer.Name, err)
		}
		catalog.installers = append(catalog.installers, installer)
	}
	for _, executor := range manifest.Executors {
		if _, err := catalog.add(executor.File); err != nil {
			return nil, fmt.Errorf("校验执行器 %s: %w", executor.Name, err)
		}
		catalog.executors = append(catalog.executors, executor)
	}
	for _, update := range manifest.Updates {
		digest, err := catalog.add(update.File)
		if err != nil {
			return nil, fmt.Errorf("校验桌面端更新包 %s: %w", update.Name, err)
		}
		signature, err := base64.StdEncoding.DecodeString(update.Signature)
		if err != nil || len(updateKey) != ed25519.PublicKeySize || !ed25519.Verify(updateKey, UpdateStatement(manifest.Version, digest), signature) {
			return nil, fmt.Errorf("校验桌面端更新包 %s: 签名无效", update.Name)
		}
		catalog.updates = append(catalog.updates, servedUpdate{Update: update, digest: digest})
	}
	catalog.version = manifest.Version
	return catalog, nil
}

// add 登记客户端目录下的一个文件，校验大小与 SHA-256 摘要并返回其 SHA-512 摘要。
func (c *Catalog) add(file release.File) ([]byte, error) {
	if PathPrefix+file.Name == UpdatePath {
		return nil, fmt.Errorf("文件名无效: %q", file.Name)
	}
	content, err := os.Open(filepath.Join(c.directory, file.Name))
	if err != nil {
		return nil, err
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != file.Size {
		return nil, fmt.Errorf("大小与发布清单记录不一致")
	}
	short, long := sha256.New(), sha512.New()
	if _, err := io.Copy(io.MultiWriter(short, long), content); err != nil {
		return nil, err
	}
	if hex.EncodeToString(short.Sum(nil)) != file.SHA256 {
		return nil, fmt.Errorf("SHA-256 摘要与发布清单记录不一致")
	}
	c.names[file.Name] = struct{}{}
	return long.Sum(nil), nil
}

// Version 返回客户端目录的版本，没有发布清单或清单版本与服务端不同时为空。
func (c *Catalog) Version() string {
	return c.version
}

// Installers 返回提供下载的安装包，按清单顺序排列。
func (c *Catalog) Installers() []release.Installer {
	return c.installers
}

// Executors 返回提供下载的无界面执行器，按清单顺序排列。
func (c *Catalog) Executors() []release.Executor {
	return c.executors
}

// URL 返回目录中文件相对部署地址的下载地址。
func URL(name, sha256Digest string) string {
	return PathPrefix + versionedName(name, sha256Digest)
}

// versionedName 在文件名后附加取自内容摘要前缀的查询参数 v，同名文件内容变化时下载地址随之变化，缓存不会返回旧内容。
func versionedName(name, sha256Digest string) string {
	return name + "?v=" + sha256Digest[:16]
}

// ServeHTTP 在 UpdatePath 输出桌面端更新清单，其余路径按文件名输出发布清单中登记的客户端文件，未登记的路径返回 404。
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
		slog.ErrorContext(request.Context(), "打开客户端文件失败", "name", name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		slog.ErrorContext(request.Context(), "读取客户端文件失败", "name", name, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// 下载地址带内容摘要，同一地址的内容不变。
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, name, info.ModTime(), content)
}

// updateManifest 是 Wails 更新器 endpoint 提供方读取的更新清单，metadata 携带覆盖版本与摘要的更新包签名。
type updateManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Version       string            `json:"version"`
	Artifacts     []updateArtifact  `json:"artifacts"`
	Metadata      map[string]string `json:"metadata"`
}

// updateArtifact 是更新清单中的一个更新包，下载地址相对清单地址解析，下载后按 SHA-512 摘要校验内容。
type updateArtifact struct {
	URL        string `json:"url"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
	DigestAlgo string `json:"digestAlgo"`
	Digest     string `json:"digest"`
}

// serveUpdate 按查询参数 platform、arch 输出服务器提供的客户端版本及对应平台的更新包，没有对应更新包时清单不含更新包，服务器没有客户端目录时返回 204；是否升级由客户端校验签名并比较版本后决定。
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
				URL: versionedName(update.Name, update.SHA256), Filename: update.Name, Size: update.Size, Platform: platform, Arch: arch,
				DigestAlgo: "sha512", Digest: base64.StdEncoding.EncodeToString(update.digest),
			}},
			Metadata: map[string]string{SignatureMetadataKey: update.Signature},
		})
		return
	}
	if c.Version() == "" {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(updateManifest{SchemaVersion: 1, Version: c.version, Artifacts: []updateArtifact{}})
}
