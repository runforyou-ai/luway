//go:build server

package servercli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/release"
)

// releaseSource 返回发布文件的来源：指定的本地目录，否则为构建品牌的发布地址。
func releaseSource(from string) (release.Source, error) {
	if from != "" {
		return release.DirSource(from), nil
	}
	build := brand.Build()
	if build.ReleaseURL == "" {
		return release.Source{}, errors.New("构建品牌没有发布地址，请用 --from 指定存放发布文件的目录")
	}
	return release.URLSource(build.ReleaseURL), nil
}

// downloadClientsCommand 取得与本程序同版本的发布清单、签名与客户端文件，校验后替换程序目录中的同名文件与客户端目录。
func downloadClientsCommand(arguments []string) error {
	flags := newFlags("download-clients", "")
	from := flags.String("from", "", "从该目录读取与本程序同版本的发布清单及客户端文件，默认从构建品牌的发布地址下载")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	directory, err := clientrelease.ProgramDirectory()
	if err != nil {
		return err
	}
	source, err := releaseSource(*from)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fetched, err := source.Fetch(ctx, buildinfo.Version, brand.Build().UpdateKey())
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(directory, ".release.*")
	if err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := writeRelease(ctx, source, fetched, staging); err != nil {
		return err
	}

	// 先删除发布清单，再替换客户端目录，最后放入新的清单与签名；中途失败时程序目录没有清单，服务端不提供客户端下载。
	for _, name := range []string{release.ManifestName, release.SignatureName} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove release manifest: %w", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(directory, clientrelease.DirName)); err != nil {
		return fmt.Errorf("remove client directory: %w", err)
	}
	for _, name := range []string{clientrelease.DirName, release.SignatureName, release.ManifestName} {
		if err := os.Rename(filepath.Join(staging, name), filepath.Join(directory, name)); err != nil {
			return fmt.Errorf("install release files: %w", err)
		}
	}
	fmt.Printf("已下载版本 %s 的发布清单与客户端文件到 %s，重启服务端后生效\n", fetched.Version, directory)
	return nil
}

// writeRelease 把发布清单与签名写入程序目录 directory，并下载清单列出的客户端文件到其 clients 子目录。
func writeRelease(ctx context.Context, source release.Source, fetched release.Fetched, directory string) error {
	if err := os.WriteFile(filepath.Join(directory, release.ManifestName), fetched.Data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, release.SignatureName), fetched.Signature, 0o644); err != nil {
		return err
	}
	clients := filepath.Join(directory, clientrelease.DirName)
	if err := os.Mkdir(clients, 0o755); err != nil {
		return err
	}
	for _, file := range fetched.ClientFiles() {
		if err := download(ctx, source, fetched.Version, file, filepath.Join(clients, file.Name)); err != nil {
			return err
		}
	}
	return nil
}

// download 从 source 取得发布文件 file 写入 target 并输出进度。
func download(ctx context.Context, source release.Source, version string, file release.File, target string) error {
	fmt.Printf("获取 %s（%.1f MB）\n", file.Name, float64(file.Size)/(1<<20))
	if err := source.Download(ctx, version, file, target); err != nil {
		return fmt.Errorf("获取 %s: %w", file.Name, err)
	}
	return nil
}
