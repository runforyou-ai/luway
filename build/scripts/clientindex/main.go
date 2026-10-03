// clientindex 从发布产物中挑出全部桌面端安装包，复制到客户端目录并生成索引；任一安装包缺失时失败。
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

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// main 按发布产物命名规则收集安装包，写入输出目录及其索引。
func main() {
	version := flag.String("version", "", "发布版本，如 0.1.0")
	source := flag.String("source", "", "发布产物目录")
	output := flag.String("output", "", "客户端目录")
	flag.Parse()
	if *version == "" || *source == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/clientindex -version <version> -source <发布产物目录> -output <客户端目录>")
		os.Exit(2)
	}
	if err := run(*version, *source, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 复制版本对应的安装包并写入索引，按展示顺序排列。
func run(version, source, output string) error {
	prefix := brand.Build().Slug + "_" + version + "_"
	candidates := []clientrelease.File{
		{OS: clientrelease.OSWindows, Arch: "amd64", Format: clientrelease.FormatEXE, Name: prefix + "windows_amd64-installer.exe"},
		{OS: clientrelease.OSWindows, Arch: "arm64", Format: clientrelease.FormatEXE, Name: prefix + "windows_arm64-installer.exe"},
		{OS: clientrelease.OSDarwin, Arch: "universal", Format: clientrelease.FormatDMG, Name: prefix + "darwin_universal.dmg"},
		{OS: clientrelease.OSLinux, Arch: "amd64", Format: clientrelease.FormatAppImage, Name: prefix + "linux_amd64.AppImage"},
		{OS: clientrelease.OSLinux, Arch: "amd64", Format: clientrelease.FormatDeb, Name: prefix + "linux_amd64.deb"},
		{OS: clientrelease.OSLinux, Arch: "amd64", Format: clientrelease.FormatRPM, Name: prefix + "linux_amd64.rpm"},
		{OS: clientrelease.OSLinux, Arch: "amd64", Format: clientrelease.FormatPacman, Name: prefix + "linux_amd64.pkg.tar.zst"},
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("创建客户端目录: %w", err)
	}
	index := clientrelease.Index{Version: version, Files: []clientrelease.File{}}
	for _, file := range candidates {
		size, digest, err := copyFile(filepath.Join(source, file.Name), filepath.Join(output, file.Name))
		if err != nil {
			return fmt.Errorf("复制安装包 %s: %w", file.Name, err)
		}
		file.Size, file.SHA256 = size, digest
		index.Files = append(index.Files, file)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, clientrelease.IndexName), append(data, '\n'), 0o644)
}

// copyFile 复制文件并返回其字节数与 SHA-256 摘要。
func copyFile(from, to string) (int64, string, error) {
	source, err := os.Open(from)
	if err != nil {
		return 0, "", err
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, hash), source)
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	return size, hex.EncodeToString(hash.Sum(nil)), err
}
