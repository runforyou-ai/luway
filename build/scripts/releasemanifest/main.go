// releasemanifest 从发布产物生成签名的发布清单：列出各平台服务端压缩包与全部桌面端安装包、更新包和无界面执行器，为更新包与清单签名，并把清单与客户端文件写入程序目录结构（清单在输出目录，客户端文件在其 clients 子目录）；任一文件缺失时失败。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/release"
)

// main 按发布产物命名规则收集服务端压缩包、安装包、更新包与执行器，写入输出目录。
func main() {
	version := flag.String("version", "", "发布版本，如 0.1.0")
	source := flag.String("source", "", "发布产物目录")
	output := flag.String("output", "", "输出目录")
	keyPath := flag.String("key", "", "签名私钥文件，PEM 编码的 PKCS #8 Ed25519 私钥")
	flag.Parse()
	if *version == "" || *source == "" || *output == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/releasemanifest -version <version> -source <发布产物目录> -output <输出目录> -key <签名私钥文件>")
		os.Exit(2)
	}
	key, err := loadKey(*keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := run(*version, *source, *output, key); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// loadKey 读取签名私钥，并确认它与构建品牌的更新签名公钥成对。
func loadKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取签名私钥: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("签名私钥不是 PEM 编码")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析签名私钥: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("签名私钥不是 Ed25519 私钥")
	}
	if !key.Public().(ed25519.PublicKey).Equal(brand.Build().UpdateKey()) {
		return nil, fmt.Errorf("签名私钥与构建品牌的更新签名公钥不匹配")
	}
	return key, nil
}

// run 复制版本对应的安装包、更新包与执行器到客户端目录，计算服务端压缩包的摘要，写入发布清单与签名；安装包与执行器按展示顺序排列。
func run(version, source, output string, key ed25519.PrivateKey) error {
	slug := brand.Build().Slug
	prefix := slug + "_" + version + "_"
	clients := filepath.Join(output, clientrelease.DirName)
	if err := os.MkdirAll(clients, 0o755); err != nil {
		return fmt.Errorf("创建客户端目录: %w", err)
	}
	manifest := release.Manifest{Version: version, Servers: []release.Server{}, Installers: []release.Installer{}, Updates: []release.Update{}, Executors: []release.Executor{}}
	// 服务端与执行器压缩包按系统与架构命名，Windows 为 zip，其余系统为 tar.gz。
	archive := func(program, system, arch string) string {
		extension := ".tar.gz"
		if system == release.OSWindows {
			extension = ".zip"
		}
		return slug + "-" + program + "_" + version + "_" + system + "_" + arch + extension
	}
	for _, system := range []string{release.OSLinux, release.OSWindows, release.OSDarwin} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := archive("server", system, arch)
			size, digest, _, err := copyFile(filepath.Join(source, name), "")
			if err != nil {
				return fmt.Errorf("读取服务端压缩包 %s: %w", name, err)
			}
			manifest.Servers = append(manifest.Servers, release.Server{OS: system, Arch: arch, File: release.File{Name: name, Size: size, SHA256: digest}})
		}
	}
	installers := []release.Installer{
		{OS: release.OSWindows, Arch: "amd64", Format: release.FormatEXE, File: release.File{Name: prefix + "windows_amd64-installer.exe"}},
		{OS: release.OSWindows, Arch: "arm64", Format: release.FormatEXE, File: release.File{Name: prefix + "windows_arm64-installer.exe"}},
		{OS: release.OSDarwin, Arch: "universal", Format: release.FormatDMG, File: release.File{Name: prefix + "darwin_universal.dmg"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatAppImage, File: release.File{Name: prefix + "linux_amd64.AppImage"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatDeb, File: release.File{Name: prefix + "linux_amd64.deb"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatRPM, File: release.File{Name: prefix + "linux_amd64.rpm"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatPacman, File: release.File{Name: prefix + "linux_amd64.pkg.tar.zst"}},
	}
	for _, installer := range installers {
		size, digest, _, err := copyFile(filepath.Join(source, installer.Name), filepath.Join(clients, installer.Name))
		if err != nil {
			return fmt.Errorf("复制安装包 %s: %w", installer.Name, err)
		}
		installer.Size, installer.SHA256 = size, digest
		manifest.Installers = append(manifest.Installers, installer)
	}
	for _, system := range []string{release.OSLinux, release.OSWindows, release.OSDarwin} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := archive("executor", system, arch)
			size, digest, _, err := copyFile(filepath.Join(source, name), filepath.Join(clients, name))
			if err != nil {
				return fmt.Errorf("复制执行器 %s: %w", name, err)
			}
			manifest.Executors = append(manifest.Executors, release.Executor{OS: system, Arch: arch, File: release.File{Name: name, Size: size, SHA256: digest}})
		}
	}
	updates := []release.Update{
		{OS: release.OSWindows, Arch: "amd64", File: release.File{Name: prefix + "windows_amd64.zip"}},
		{OS: release.OSWindows, Arch: "arm64", File: release.File{Name: prefix + "windows_arm64.zip"}},
		{OS: release.OSDarwin, Arch: "universal", File: release.File{Name: prefix + "darwin_universal.zip"}},
	}
	for _, update := range updates {
		size, digest, longDigest, err := copyFile(filepath.Join(source, update.Name), filepath.Join(clients, update.Name))
		if err != nil {
			return fmt.Errorf("复制更新包 %s: %w", update.Name, err)
		}
		signature := ed25519.Sign(key, clientrelease.UpdateStatement(version, longDigest))
		update.Size, update.SHA256, update.Signature = size, digest, base64.StdEncoding.EncodeToString(signature)
		manifest.Updates = append(manifest.Updates, update)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(output, release.ManifestName), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, release.SignatureName), release.Sign(key, data), 0o644)
}

// copyFile 复制文件并返回其字节数、十六进制 SHA-256 摘要与 SHA-512 摘要，to 为空时只计算摘要。
func copyFile(from, to string) (int64, string, []byte, error) {
	source, err := os.Open(from)
	if err != nil {
		return 0, "", nil, err
	}
	defer source.Close()
	if to == "" {
		to = os.DevNull
	}
	target, err := os.Create(to)
	if err != nil {
		return 0, "", nil, err
	}
	short, long := sha256.New(), sha512.New()
	size, err := io.Copy(io.MultiWriter(target, short, long), source)
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	return size, hex.EncodeToString(short.Sum(nil)), long.Sum(nil), err
}
