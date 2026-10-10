package release

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runforyou-ai/support"
)

// downloadAttempts 是从发布地址下载一个文件的最多尝试次数。
const downloadAttempts = 3

// Source 是发布文件的来源：按版本区分的发布地址，或存放一个版本全部发布文件的本地目录。
type Source struct {
	directory  string
	versionURL string
	client     *http.Client
}

// Fetched 是从来源取得并通过签名校验的发布清单及其原文。
type Fetched struct {
	Manifest
	// Data 是清单原文。
	Data []byte
	// Signature 是清单签名文件的内容。
	Signature []byte
}

// DirSource 返回读取本地目录 directory 的来源。
func DirSource(directory string) Source {
	return Source{directory: directory}
}

// URLSource 返回从发布地址下载的来源：versionURL 中的 {version} 替换为版本号。
func URLSource(versionURL string) Source {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return Source{versionURL: versionURL, client: &http.Client{Transport: transport}}
}

// String 返回来源的展示文本。
func (s Source) String() string {
	if s.directory != "" {
		return s.directory
	}
	return s.versionURL
}

// Fetch 取得 version 版本的发布清单并用 key 校验签名。
func (s Source) Fetch(ctx context.Context, version string, key ed25519.PublicKey) (Fetched, error) {
	var data, signature []byte
	var err error
	switch {
	case s.directory != "":
		data, err = os.ReadFile(filepath.Join(s.directory, ManifestName))
		if err == nil {
			signature, err = os.ReadFile(filepath.Join(s.directory, SignatureName))
		}
	default:
		base := s.versionBase(version)
		data, err = s.get(ctx, base, ManifestName)
		if err == nil {
			signature, err = s.get(ctx, base, SignatureName)
		}
	}
	if err != nil {
		return Fetched{}, fmt.Errorf("读取发布清单: %w", err)
	}
	manifest, err := Parse(data, signature, key)
	if err != nil {
		return Fetched{}, err
	}
	if manifest.Version != version {
		return Fetched{}, fmt.Errorf("发布清单版本 %s 与要求的版本 %s 不同", manifest.Version, version)
	}
	return Fetched{Manifest: manifest, Data: data, Signature: signature}, nil
}

// Download 把 version 版本的发布文件 file 写入 target，并校验大小与 SHA-256 摘要；从发布地址下载失败时重试。
func (s Source) Download(ctx context.Context, version string, file File, target string) error {
	if s.directory != "" {
		input, err := os.Open(filepath.Join(s.directory, file.Name))
		if err != nil {
			return err
		}
		defer input.Close()
		return writeVerified(input, file, target)
	}
	// 从发布地址下载，失败时重试。
	return support.Retry(ctx, downloadAttempts, func(int) error {
		response, err := s.open(ctx, s.versionBase(version), file.Name)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		return writeVerified(response.Body, file, target)
	})
}

// versionBase 返回 version 版本的发布文件地址。
func (s Source) versionBase(version string) string {
	return strings.ReplaceAll(s.versionURL, "{version}", url.PathEscape(version))
}

// get 读取发布地址 base 下的小文件。
func (s Source) get(ctx context.Context, base, name string) ([]byte, error) {
	response, err := s.open(ctx, base, name)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// 清单与签名不超过 4 MiB。
	return io.ReadAll(io.LimitReader(response.Body, 4<<20))
}

// open 请求发布地址 base 下的文件 name，响应状态不是 200 时报错。
func (s Source) open(ctx context.Context, base, name string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("下载 %s: %s", request.URL, response.Status)
	}
	return response, nil
}

// writeVerified 把 input 写入 target 并校验大小与摘要，校验失败时删除 target。
func writeVerified(input io.Reader, file File, target string) error {
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	digest := sha256.New()
	// 多读一个字节以发现超出清单记录的内容。
	size, err := io.Copy(io.MultiWriter(output, digest), io.LimitReader(input, file.Size+1))
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err == nil && size != file.Size {
		err = fmt.Errorf("%s 大小与发布清单记录不一致", file.Name)
	}
	if err == nil && hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
		err = fmt.Errorf("%s 的 SHA-256 摘要与发布清单记录不一致", file.Name)
	}
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}
