// clientindex 从发布产物中挑出全部桌面端安装包与更新包，复制到客户端目录并生成索引；品牌配置了更新公钥时用对应私钥签名更新包，否则不收录更新包。任一文件缺失时失败。
package main

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// main 按发布产物命名规则收集安装包与更新包，写入输出目录及其索引。
func main() {
	version := flag.String("version", "", "发布版本，如 0.1.0")
	source := flag.String("source", "", "发布产物目录")
	output := flag.String("output", "", "客户端目录")
	key := flag.String("key", "", "更新签名私钥文件（wails3 updater genkey 生成的 PEM），品牌配置了 updatePublicKey 时必填")
	flag.Parse()
	if *version == "" || *source == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/clientindex -version <version> -source <发布产物目录> -output <客户端目录> [-key <更新签名私钥>]")
		os.Exit(2)
	}
	if err := run(*version, *source, *output, *key, brand.Build().UpdateKey()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 复制版本对应的安装包与更新包并写入索引，按展示顺序排列；publicKey 是品牌的更新公钥，为空时不收录更新包。
func run(version, source, output, keyPath string, publicKey ed25519.PublicKey) error {
	signer, err := loadSigner(keyPath, publicKey)
	if err != nil {
		return err
	}
	prefix := brand.Build().Slug + "_" + version + "_"
	// installer 返回发布产物中的一个安装包。
	installer := func(goos, arch, format, suffix string) clientrelease.Installer {
		return clientrelease.Installer{OS: goos, Arch: arch, Format: format, File: clientrelease.File{Name: prefix + suffix}}
	}
	index := clientrelease.Index{
		Version: version,
		Installers: []clientrelease.Installer{
			installer(clientrelease.OSWindows, "amd64", clientrelease.FormatEXE, "windows_amd64-installer.exe"),
			installer(clientrelease.OSWindows, "arm64", clientrelease.FormatEXE, "windows_arm64-installer.exe"),
			installer(clientrelease.OSDarwin, clientrelease.ArchUniversal, clientrelease.FormatDMG, "darwin_universal.dmg"),
			installer(clientrelease.OSLinux, "amd64", clientrelease.FormatAppImage, "linux_amd64.AppImage"),
			installer(clientrelease.OSLinux, "amd64", clientrelease.FormatDeb, "linux_amd64.deb"),
			installer(clientrelease.OSLinux, "amd64", clientrelease.FormatRPM, "linux_amd64.rpm"),
			installer(clientrelease.OSLinux, "amd64", clientrelease.FormatPacman, "linux_amd64.pkg.tar.zst"),
		},
		Updates: []clientrelease.Update{},
	}
	if signer != nil {
		index.Updates = []clientrelease.Update{
			{OS: clientrelease.OSWindows, Arch: "amd64", File: clientrelease.File{Name: prefix + "windows_amd64.exe"}},
			{OS: clientrelease.OSWindows, Arch: "arm64", File: clientrelease.File{Name: prefix + "windows_arm64.exe"}},
			{OS: clientrelease.OSDarwin, Arch: clientrelease.ArchUniversal, File: clientrelease.File{Name: prefix + "darwin_universal.zip"}},
		}
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("创建客户端目录: %w", err)
	}
	for i := range index.Installers {
		if _, err := copyFile(source, output, &index.Installers[i].File); err != nil {
			return err
		}
	}
	for i := range index.Updates {
		digest, err := copyFile(source, output, &index.Updates[i].File)
		if err != nil {
			return err
		}
		if index.Updates[i].OS == clientrelease.OSDarwin {
			if err := checkBundleArchive(filepath.Join(output, index.Updates[i].Name)); err != nil {
				return err
			}
		}
		signature, err := signer.Sign(nil, digest, &ed25519.Options{Hash: crypto.SHA512})
		if err != nil {
			return fmt.Errorf("签名更新包 %s: %w", index.Updates[i].Name, err)
		}
		index.Updates[i].Signature = base64.StdEncoding.EncodeToString(signature)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, clientrelease.IndexName), append(data, '\n'), 0o644)
}

// loadSigner 读取更新签名私钥并核对它与品牌的更新公钥配对；品牌未配置更新公钥时不签名，此时不得传入私钥。
func loadSigner(keyPath string, publicKey ed25519.PublicKey) (ed25519.PrivateKey, error) {
	if publicKey == nil {
		if keyPath != "" {
			return nil, errors.New("品牌未配置 updatePublicKey，不能签名更新包")
		}
		return nil, nil
	}
	if keyPath == "" {
		return nil, errors.New("品牌配置了 updatePublicKey，必须用 -key 传入对应的更新签名私钥")
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("读取更新签名私钥: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("更新签名私钥不是 PEM 格式")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析更新签名私钥: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || !bytes.Equal(privateKey.Public().(ed25519.PublicKey), publicKey) {
		return nil, errors.New("更新签名私钥与品牌的 updatePublicKey 不配对")
	}
	return privateKey, nil
}

// checkBundleArchive 校验 macOS 更新包的根目录只含一个应用包，更新器只能用单一根条目替换应用。
func checkBundleArchive(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("读取更新包 %s: %w", filepath.Base(path), err)
	}
	defer archive.Close()
	roots := map[string]bool{}
	for _, file := range archive.File {
		roots[strings.SplitN(file.Name, "/", 2)[0]] = true
	}
	if len(roots) != 1 {
		return fmt.Errorf("更新包 %s 的根目录应只含一个应用包，实际有 %d 项", filepath.Base(path), len(roots))
	}
	for root := range roots {
		if !strings.HasSuffix(root, ".app") {
			return fmt.Errorf("更新包 %s 的根目录 %s 不是应用包", filepath.Base(path), root)
		}
	}
	return nil
}

// copyFile 把发布产物复制到客户端目录，填写文件的字节数与 SHA-512 摘要并返回原始摘要。
func copyFile(source, output string, file *clientrelease.File) ([]byte, error) {
	from, err := os.Open(filepath.Join(source, file.Name))
	if err != nil {
		return nil, fmt.Errorf("复制 %s: %w", file.Name, err)
	}
	defer from.Close()
	to, err := os.Create(filepath.Join(output, file.Name))
	if err != nil {
		return nil, fmt.Errorf("复制 %s: %w", file.Name, err)
	}
	hash := sha512.New()
	size, err := io.Copy(io.MultiWriter(to, hash), from)
	if closeErr := to.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("复制 %s: %w", file.Name, err)
	}
	digest := hash.Sum(nil)
	file.Size, file.SHA512 = size, hex.EncodeToString(digest)
	return digest, nil
}
