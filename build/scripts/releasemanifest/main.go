// releasemanifest 从发布产物生成发布清单：列出各平台服务端压缩包与全部桌面端安装包、更新包和无界面执行器及其摘要，并把清单与客户端文件写入程序目录结构（清单在输出目录，客户端文件在其 clients 子目录）；任一文件缺失时失败。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/release"
	releaseclients "github.com/runforyou-ai/luway/internal/release/clients"
)

// main 按发布产物命名规则收集服务端压缩包、安装包、更新包与执行器，写入输出目录。
func main() {
	version := flag.String("version", "", "发布版本，如 0.1.0")
	source := flag.String("source", "", "发布产物目录")
	output := flag.String("output", "", "输出目录")
	desktopPlatforms := flag.String("desktop-platforms", "windows,darwin,linux", "本次发布的桌面平台，逗号分隔；所选平台的全部产物必须存在")
	flag.Parse()
	if *version == "" || *source == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/releasemanifest -version <version> -source <发布产物目录> -output <输出目录>")
		os.Exit(2)
	}
	if err := run(*version, *source, *output, *desktopPlatforms); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 复制版本对应的安装包、更新包与执行器到客户端目录，计算服务端压缩包的摘要，写入发布清单；安装包与执行器按展示顺序排列。
func run(version, source, output, desktopPlatforms string) error {
	platforms := make(map[string]bool)
	for _, platform := range strings.Split(desktopPlatforms, ",") {
		platform = strings.TrimSpace(platform)
		switch platform {
		case release.OSWindows, release.OSDarwin, release.OSLinux:
			platforms[platform] = true
		default:
			return fmt.Errorf("无效的桌面发布平台 %q", platform)
		}
	}
	slug := brand.Build().Slug
	prefix := slug + "_" + version + "_"
	clients := filepath.Join(output, releaseclients.DirName)
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
	// 服务端只发布 Linux amd64/arm64。
	serverTargets := []struct{ OS, Arch string }{
		{release.OSLinux, "amd64"},
		{release.OSLinux, "arm64"},
	}
	for _, target := range serverTargets {
		name := archive("server", target.OS, target.Arch)
		size, digest, err := copyFile(filepath.Join(source, name), "")
		if err != nil {
			return fmt.Errorf("读取服务端压缩包 %s: %w", name, err)
		}
		manifest.Servers = append(manifest.Servers, release.Server{OS: target.OS, Arch: target.Arch, File: release.File{Name: name, Size: size, SHA256: digest}})
	}
	installers := []release.Installer{
		{OS: release.OSWindows, Arch: "amd64", Format: release.FormatEXE, File: release.File{Name: prefix + "windows_amd64-installer.exe"}},
		{OS: release.OSDarwin, Arch: "arm64", Format: release.FormatDMG, File: release.File{Name: prefix + "darwin_arm64.dmg"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatAppImage, File: release.File{Name: prefix + "linux_amd64.AppImage"}},
		{OS: release.OSLinux, Arch: "arm64", Format: release.FormatAppImage, File: release.File{Name: prefix + "linux_arm64.AppImage"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatDeb, File: release.File{Name: prefix + "linux_amd64.deb"}},
		{OS: release.OSLinux, Arch: "arm64", Format: release.FormatDeb, File: release.File{Name: prefix + "linux_arm64.deb"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatRPM, File: release.File{Name: prefix + "linux_amd64.rpm"}},
		{OS: release.OSLinux, Arch: "arm64", Format: release.FormatRPM, File: release.File{Name: prefix + "linux_arm64.rpm"}},
		{OS: release.OSLinux, Arch: "amd64", Format: release.FormatPacman, File: release.File{Name: prefix + "linux_amd64.pkg.tar.zst"}},
		{OS: release.OSLinux, Arch: "arm64", Format: release.FormatPacman, File: release.File{Name: prefix + "linux_arm64.pkg.tar.zst"}},
	}
	for _, installer := range installers {
		if !platforms[installer.OS] {
			continue
		}
		size, digest, err := copyFile(filepath.Join(source, installer.Name), filepath.Join(clients, installer.Name))
		if err != nil {
			return fmt.Errorf("复制安装包 %s: %w", installer.Name, err)
		}
		installer.Size, installer.SHA256 = size, digest
		manifest.Installers = append(manifest.Installers, installer)
	}
	for _, system := range []string{release.OSLinux, release.OSWindows, release.OSDarwin} {
		for _, arch := range []string{"amd64", "arm64"} {
			// 暂不发布 Windows arm64、macOS Intel（amd64）执行器。
			if system == release.OSWindows && arch == "arm64" {
				continue
			}
			if system == release.OSDarwin && arch == "amd64" {
				continue
			}
			name := archive("executor", system, arch)
			size, digest, err := copyFile(filepath.Join(source, name), filepath.Join(clients, name))
			if err != nil {
				return fmt.Errorf("复制执行器 %s: %w", name, err)
			}
			manifest.Executors = append(manifest.Executors, release.Executor{OS: system, Arch: arch, File: release.File{Name: name, Size: size, SHA256: digest}})
		}
	}
	updates := []release.Update{
		{OS: release.OSWindows, Arch: "amd64", File: release.File{Name: prefix + "windows_amd64.zip"}},
		{OS: release.OSDarwin, Arch: "arm64", File: release.File{Name: prefix + "darwin_arm64.zip"}},
	}
	for _, update := range updates {
		if !platforms[update.OS] {
			continue
		}
		size, digest, err := copyFile(filepath.Join(source, update.Name), filepath.Join(clients, update.Name))
		if err != nil {
			return fmt.Errorf("复制更新包 %s: %w", update.Name, err)
		}
		update.Size, update.SHA256 = size, digest
		manifest.Updates = append(manifest.Updates, update)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(output, release.ManifestName), data, 0o644)
}

// copyFile 复制文件并返回其字节数与十六进制 SHA-256 摘要，to 为空时只计算摘要。
func copyFile(from, to string) (int64, string, error) {
	source, err := os.Open(from)
	if err != nil {
		return 0, "", err
	}
	defer source.Close()
	if to == "" {
		to = os.DevNull
	}
	target, err := os.Create(to)
	if err != nil {
		return 0, "", err
	}
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, digest), source)
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	return size, hex.EncodeToString(digest.Sum(nil)), err
}
