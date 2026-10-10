// Package release 定义发布清单：一个版本的各平台服务端压缩包、客户端安装包、桌面端更新包与无界面执行器及其大小和摘要，清单以品牌私钥签名。
package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
)

const (
	// ManifestName 是发布清单的文件名。
	ManifestName = "release.json"
	// SignatureName 是发布清单签名的文件名，内容是对清单原文的 Ed25519 签名，取标准 Base64 编码。
	SignatureName = "release.json.sig"
)

// 发布文件的操作系统。
const (
	OSWindows = "windows"
	OSDarwin  = "darwin"
	OSLinux   = "linux"
)

// 客户端安装包格式。
const (
	FormatEXE      = "exe"
	FormatDMG      = "dmg"
	FormatAppImage = "appimage"
	FormatDeb      = "deb"
	FormatRPM      = "rpm"
	FormatPacman   = "pacman"
)

// Manifest 是一个版本的发布清单。
type Manifest struct {
	// Version 是清单中全部文件的版本。
	Version string `json:"version"`
	// Servers 是各平台的服务端压缩包：Windows 为 zip，其余系统为 tar.gz，内含单个服务端程序。
	Servers []Server `json:"servers"`
	// Installers 是客户端安装包，按展示顺序排列。
	Installers []Installer `json:"installers"`
	// Updates 是桌面端更新包。
	Updates []Update `json:"updates"`
	// Executors 是无界面执行器压缩包，按展示顺序排列。
	Executors []Executor `json:"executors"`
}

// File 是发布清单中的一个文件。
type File struct {
	// Name 是文件名，不含目录。
	Name string `json:"name"`
	// Size 是文件的字节数。
	Size int64 `json:"size"`
	// SHA256 是文件内容的十六进制 SHA-256 摘要。
	SHA256 string `json:"sha256"`
}

// Server 是一个服务端压缩包。
type Server struct {
	// OS 是服务端的操作系统：windows、darwin 或 linux。
	OS string `json:"os"`
	// Arch 是服务端的处理器架构：amd64 或 arm64。
	Arch string `json:"arch"`
	File
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

// Update 是一个桌面端更新包，内含替换已安装程序的应用包或可执行文件。
type Update struct {
	// OS 是更新包的操作系统：windows 或 darwin。
	OS string `json:"os"`
	// Arch 是更新包的处理器架构：amd64、arm64 或 universal。
	Arch string `json:"arch"`
	File
	// Signature 是对客户端版本与更新包 SHA-512 摘要的 Ed25519 签名，取标准 Base64 编码，供桌面端更新器校验。
	Signature string `json:"signature"`
}

// Executor 是一个无界面执行器压缩包，内含单个静态程序：Windows 为 zip，其余系统为 tar.gz。
type Executor struct {
	// OS 是执行器的操作系统：windows、darwin 或 linux。
	OS string `json:"os"`
	// Arch 是执行器的处理器架构：amd64 或 arm64。
	Arch string `json:"arch"`
	File
}

// ClientFiles 返回清单列出的安装包、更新包与执行器。
func (m Manifest) ClientFiles() []File {
	return slices.Concat(
		arr.Map(m.Installers, func(installer Installer) File { return installer.File }),
		arr.Map(m.Updates, func(update Update) File { return update.File }),
		arr.Map(m.Executors, func(executor Executor) File { return executor.File }),
	)
}

// Sign 返回清单原文 data 的签名文件内容。
func Sign(key ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n")
}

// Parse 用 key 校验清单原文 data 的签名 signature，再解析清单并校验文件名。
func Parse(data, signature []byte, key ed25519.PublicKey) (Manifest, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, decoded) {
		return Manifest{}, errors.New("发布清单签名无效")
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析发布清单: %w", err)
	}
	if manifest.Version == "" || filepath.Base(manifest.Version) != manifest.Version || strings.HasPrefix(manifest.Version, ".") {
		return Manifest{}, fmt.Errorf("发布清单中的版本无效: %q", manifest.Version)
	}
	var names set.Set[string]
	for _, file := range append(manifest.ClientFiles(), arr.Map(manifest.Servers, func(server Server) File { return server.File })...) {
		// 文件名只能是单层文件名，且在清单中唯一。
		if file.Name == "" || filepath.Base(file.Name) != file.Name || strings.ContainsAny(file.Name, `/\`) || strings.HasPrefix(file.Name, ".") || file.Name == ManifestName || file.Name == SignatureName {
			return Manifest{}, fmt.Errorf("发布清单中的文件名无效: %q", file.Name)
		}
		if !names.Add(file.Name) {
			return Manifest{}, fmt.Errorf("发布清单中的文件名重复: %q", file.Name)
		}
	}
	return manifest, nil
}

// ReadDir 读取并校验 directory 中的发布清单与签名，清单不存在时返回的错误匹配 fs.ErrNotExist。
func ReadDir(directory string, key ed25519.PublicKey) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(directory, ManifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("读取发布清单: %w", err)
	}
	signature, err := os.ReadFile(filepath.Join(directory, SignatureName))
	// 清单存在而签名缺失时视为签名无效，错误不匹配 fs.ErrNotExist。
	if err != nil {
		return Manifest{}, fmt.Errorf("读取发布清单签名: %v", err)
	}
	return Parse(data, signature, key)
}
