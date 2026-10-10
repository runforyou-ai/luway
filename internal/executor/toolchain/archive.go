package toolchain

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runforyou-ai/support/filex"
)

// downloadStallTimeout 是下载在建立连接、等待响应或传输过程中没有任何进展即放弃的时限。
var downloadStallTimeout = time.Minute

// errDownloadStalled 表示下载超过时限没有进展。
var errDownloadStalled = errors.New("download stalled")

// progressReader 在每次读到数据时调用 onProgress。
type progressReader struct {
	reader     io.Reader
	onProgress func()
}

// Read 读取数据并在读到内容时报告进展。
func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.onProgress()
	}
	return n, err
}

// download 把发行物下载到缓存目录并校验 SHA256，缓存中已有校验一致的文件时直接复用，返回文件路径；超过时限没有进展时放弃。
func download(ctx context.Context, client *http.Client, url, cacheDir string, item artifact) (string, error) {
	target := filepath.Join(cacheDir, item.file)
	if checksum(target) == item.sha256 {
		return target, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(downloadStallTimeout, func() { cancel(errDownloadStalled) })
	defer stall.Stop()
	response, err := get(ctx, client, url, "*/*")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	partial, err := os.CreateTemp(cacheDir, item.file+".*.part")
	if err != nil {
		return "", fmt.Errorf("create download file: %w", err)
	}
	defer os.Remove(partial.Name())
	hash := sha256.New()
	body := &progressReader{reader: response.Body, onProgress: func() { stall.Reset(downloadStallTimeout) }}
	_, copyErr := io.Copy(io.MultiWriter(partial, hash), body)
	closeErr := partial.Close()
	if copyErr != nil {
		return "", &stepError{failure: FailureDownload, err: fmt.Errorf("download %s: %w", url, cmp.Or(context.Cause(ctx), copyErr))}
	}
	if closeErr != nil {
		return "", fmt.Errorf("download %s: %w", url, closeErr)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != item.sha256 {
		return "", &stepError{failure: FailureVerify, err: fmt.Errorf("download %s: SHA256 mismatch, got %s", url, actual)}
	}
	if err := os.Rename(partial.Name(), target); err != nil {
		return "", fmt.Errorf("store download: %w", err)
	}
	return target, nil
}

// checksum 返回文件的 SHA256，文件不可读时返回空串。
func checksum(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// install 把压缩包解压到临时目录后原子移动为 target：content 非空时以压缩包内该目录的内容为准，
// 否则压缩包只有一个顶层目录时以该目录的内容为准。
func install(archivePath, target, content string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create toolchain directory: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".staging-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if strings.HasSuffix(archivePath, ".zip") || strings.HasSuffix(archivePath, ".whl") {
		err = filex.ExtractZip(archivePath, staging)
	} else {
		err = filex.ExtractTarGz(archivePath, staging)
	}
	if err != nil {
		return fmt.Errorf("extract %s: %w", filepath.Base(archivePath), err)
	}
	source, err := filex.EntryPath(staging, content)
	if err != nil {
		return err
	}
	// 未指定目录时，发行物常把全部内容放在一个顶层目录中。
	if entries, err := os.ReadDir(staging); content == "" && err == nil && len(entries) == 1 && entries[0].IsDir() {
		source = filepath.Join(staging, entries[0].Name())
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("activate toolchain directory: %w", err)
	}
	return nil
}
