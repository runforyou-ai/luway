// clientindex 从发布产物中挑出全部桌面端安装包与更新包，复制到客户端目录，为更新包签名并生成索引；任一文件缺失时失败。
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
)

// main 按发布产物命名规则收集安装包与更新包，写入输出目录及其索引。
func main() {
	version := flag.String("version", "", "发布版本，如 0.1.0")
	source := flag.String("source", "", "发布产物目录")
	output := flag.String("output", "", "客户端目录")
	keyPath := flag.String("key", "", "更新包签名私钥文件，PEM 编码的 PKCS #8 Ed25519 私钥")
	flag.Parse()
	if *version == "" || *source == "" || *output == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/clientindex -version <version> -source <发布产物目录> -output <客户端目录> -key <签名私钥文件>")
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

// loadKey 读取更新包签名私钥，并确认它与构建品牌的更新签名公钥成对。
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

// run 复制版本对应的安装包与更新包并写入索引，安装包按展示顺序排列。
func run(version, source, output string, key ed25519.PrivateKey) error {
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
	updates := []clientrelease.Update{
		{OS: clientrelease.OSWindows, Arch: "amd64", Name: prefix + "windows_amd64.zip"},
		{OS: clientrelease.OSWindows, Arch: "arm64", Name: prefix + "windows_arm64.zip"},
		{OS: clientrelease.OSDarwin, Arch: "universal", Name: prefix + "darwin_universal.zip"},
	}
	index := clientrelease.Index{Version: version, Files: []clientrelease.File{}, Updates: []clientrelease.Update{}}
	for _, file := range candidates {
		size, digest, _, err := copyFile(filepath.Join(source, file.Name), filepath.Join(output, file.Name))
		if err != nil {
			return fmt.Errorf("复制安装包 %s: %w", file.Name, err)
		}
		file.Size, file.SHA256 = size, digest
		index.Files = append(index.Files, file)
	}
	for _, update := range updates {
		size, digest, longDigest, err := copyFile(filepath.Join(source, update.Name), filepath.Join(output, update.Name))
		if err != nil {
			return fmt.Errorf("复制更新包 %s: %w", update.Name, err)
		}
		signature := ed25519.Sign(key, clientrelease.UpdateStatement(version, longDigest))
		update.Size, update.SHA256, update.Signature = size, digest, base64.StdEncoding.EncodeToString(signature)
		index.Updates = append(index.Updates, update)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, clientrelease.IndexName), append(data, '\n'), 0o644)
}

// copyFile 复制文件并返回其字节数、十六进制 SHA-256 摘要与 SHA-512 摘要。
func copyFile(from, to string) (int64, string, []byte, error) {
	source, err := os.Open(from)
	if err != nil {
		return 0, "", nil, err
	}
	defer source.Close()
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
