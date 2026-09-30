package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// tarEntry 是测试压缩包中的一个条目。
type tarEntry struct {
	name     string
	content  string
	linkname string
}

// buildTarGz 生成包含指定条目的 tar.gz 内容。
func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.content)), Typeflag: tar.TypeReg}
		if entry.linkname != "" {
			header = &tar.Header{Name: entry.name, Linkname: entry.linkname, Typeflag: tar.TypeSymlink}
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// serveArchive 启动返回固定内容的下载服务，并返回下载地址与内容的发行物描述。
func serveArchive(t *testing.T, data []byte, file string) (string, artifact) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	sum := sha256.Sum256(data)
	return server.URL + "/" + file, artifact{file: file, sha256: hex.EncodeToString(sum[:])}
}

// TestInstallDistFlattensTopDirectory 验证发行物下载校验后解压到版本目录，单个顶层目录被展开且保留相对符号链接。
func TestInstallDistFlattensTopDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接需要额外权限")
	}
	data := buildTarGz(t, []tarEntry{
		{name: "node-v1/lib/cli.js", content: "console.log(1)"},
		{name: "node-v1/bin/npm", linkname: "../lib/cli.js"},
	})
	url, item := serveArchive(t, data, "node.tar.gz")
	manager := New(filepath.Join(t.TempDir(), "toolchains"), t.TempDir(), func() {})
	resolve := func(context.Context) (string, error) { return url, nil }
	if err := manager.installDist(context.Background(), "node", "1.0.0", resolve, item, ""); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(manager.root, "dist", "node", "1.0.0", "bin", "npm"))
	if err != nil || string(content) != "console.log(1)" {
		t.Fatalf("解压结果不符合预期: %q %v", content, err)
	}
}

// TestDownloadRejectsChecksumMismatch 验证 SHA256 不一致的下载被拒绝且不留在缓存中。
func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	url, item := serveArchive(t, []byte("tampered"), "uv.tar.gz")
	item.sha256 = strings.Repeat("0", 64)
	cache := t.TempDir()
	_, err := download(context.Background(), http.DefaultClient, url, cache, item)
	if step, ok := errors.AsType[*stepError](err); !ok || step.failure != FailureVerify {
		t.Fatalf("未以校验失败拒绝下载: %v", err)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("缓存残留文件: %v", entries)
	}
}

// TestActiveVersionAndStaleCleanup 验证命令使用语义化版本最高的已安装版本，只清理没有进程使用的较低版本。
func TestActiveVersionAndStaleCleanup(t *testing.T) {
	root := t.TempDir()
	// 目录在管理器创建之后建立，创建时的清理不涉及这些版本。
	running := New(root, t.TempDir(), func() {})
	for _, version := range []string{"0.9.0", "0.10.0"} {
		if err := os.MkdirAll(filepath.Join(root, "dist", "uv", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if version := running.activeVersion("uv"); version != "0.10.0" {
		t.Fatalf("应使用最高的已安装版本，实际 %q", version)
	}
	if running.usable() {
		t.Fatal("缺少 Node.js 与默认 Python 时不应可用")
	}
	running.use("uv", "0.9.0")
	New(root, t.TempDir(), func() {})
	if !dirExists(filepath.Join(root, "dist", "uv", "0.9.0")) {
		t.Fatal("有进程使用的旧版本不应清理")
	}
	running.Close()
	New(root, t.TempDir(), func() {})
	if dirExists(filepath.Join(root, "dist", "uv", "0.9.0")) || !dirExists(filepath.Join(root, "dist", "uv", "0.10.0")) {
		t.Fatal("使用方退出后应只清理较低版本")
	}
}

// TestDownloadGivesUpWhenStalled 验证下载超过时限没有进展时放弃并标为下载失败。
func TestDownloadGivesUpWhenStalled(t *testing.T) {
	previous := downloadStallTimeout
	downloadStallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { downloadStallTimeout = previous })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	_, err := download(context.Background(), http.DefaultClient, server.URL, t.TempDir(), artifact{file: "uv.tar.gz", sha256: strings.Repeat("0", 64)})
	if step, ok := errors.AsType[*stepError](err); !ok || step.failure != FailureDownload || !errors.Is(err, errDownloadStalled) {
		t.Fatalf("停滞的下载未按下载失败放弃: %v", err)
	}
}

// TestRetryIntervalIsCapped 验证连续失败多次后重试间隔保持在上限。
func TestRetryIntervalIsCapped(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	manager := New(t.TempDir(), t.TempDir(), func() {})
	t.Cleanup(manager.Close)
	manager.failures = 99
	manager.sources = &Sources{PyPIIndexURL: server.URL}
	manager.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	manager.preparing, manager.stopPrepare, manager.prepareDone = true, cancel, make(chan struct{})
	manager.prepare(ctx)
	if wait := time.Until(manager.retryAt); wait < retryMaxInterval-time.Minute || wait > retryMaxInterval {
		t.Fatalf("重试间隔 %v 未保持在上限", wait)
	}
}

// TestEnvironmentConfinesInstallsAndInjectsMirrors 验证命令环境把安装位置限定在工具链目录并注入镜像。
func TestEnvironmentConfinesInstallsAndInjectsMirrors(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	manager := New(root, cache, func() {})
	environment := manager.environment(Sources{PyPIIndexURL: "https://pypi.example.com/simple"}, "0.1.0", "1.0.0")
	bin := filepath.Join(root, "bin")
	for _, expected := range []string{
		"UV_PYTHON_BIN_DIR=" + bin, "UV_TOOL_BIN_DIR=" + bin, "UV_PYTHON_INSTALL_REGISTRY=0", "PIP_REQUIRE_VIRTUALENV=true",
		"UV_PYTHON_PREFERENCE=managed", "UV_DEFAULT_INDEX=https://pypi.example.com/simple",
	} {
		if !slices.Contains(environment.Variables, expected) {
			t.Fatalf("缺少环境变量 %s: %v", expected, environment.Variables)
		}
	}
	if slices.ContainsFunc(environment.Variables, func(v string) bool { return strings.HasPrefix(v, "NPM_CONFIG_REGISTRY=") }) {
		t.Fatal("未配置的镜像不应注入")
	}
	if environment.PathPrefix[0] != filepath.Join(root, "dist", "uv", "0.1.0") || !slices.Contains(environment.PathPrefix, bin) {
		t.Fatalf("PATH 前缀不符合预期: %v", environment.PathPrefix)
	}
}

// TestEnsureBacksOffAfterFailure 验证准备开始与失败时通知调用方、状态给出失败原因，并在退避期内跳过重试。
func TestEnsureBacksOffAfterFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	changes := make(chan struct{}, 2)
	manager := New(t.TempDir(), t.TempDir(), func() { changes <- struct{}{} })
	t.Cleanup(manager.Close)
	manager.sources = &Sources{PyPIIndexURL: server.URL}
	if manager.Ensure() {
		t.Fatal("运行环境未准备时不应可用")
	}
	for range 2 {
		select {
		case <-changes:
		case <-time.After(10 * time.Second):
			t.Fatal("准备开始或结束时未通知")
		}
	}
	if status := manager.Status(); status.State != StateFailed || status.Failure != FailureDownload {
		t.Fatalf("状态不符合预期: %+v", status)
	}
	manager.Ensure()
	manager.mu.Lock()
	preparing, retryAt := manager.preparing, manager.retryAt
	manager.mu.Unlock()
	if preparing || time.Until(retryAt) < retryBaseInterval/2 {
		t.Fatalf("退避期内不应重试: preparing=%v retryAt=%v", preparing, retryAt)
	}
}

// TestIndexLinksResolvesRelativeLinks 验证简单索引中的相对链接按项目页解析，摘要取自链接片段，索引不可用时标为下载失败。
func TestIndexLinksResolvesRelativeLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/simple/uv/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`<html><body>
<a href="../../packages/aa/uv-0.1.0-py3-none-any.whl#sha256=00">uv-0.1.0-py3-none-any.whl</a><br/>
<a href="../../packages/bb/uv-0.2.0-py3-none-any.whl#sha256=11" data-requires-python="&gt;=3.8">uv-0.2.0-py3-none-any.whl</a>
</body></html>`))
	}))
	t.Cleanup(server.Close)
	links, err := indexLinks(context.Background(), http.DefaultClient, server.URL+"/simple", "uv")
	if err != nil || len(links) != 2 || links[1] != (indexLink{file: "uv-0.2.0-py3-none-any.whl", url: server.URL + "/packages/bb/uv-0.2.0-py3-none-any.whl", sha256: "11"}) {
		t.Fatalf("索引链接不符合预期: %+v %v", links, err)
	}
	_, err = indexLinks(context.Background(), http.DefaultClient, server.URL+"/missing", "uv")
	if FailureOf(err) != FailureDownload {
		t.Fatalf("索引不可用应标为下载失败: %v", err)
	}
}

// TestInstallUsesWheelContentDirectory 验证 wheel 以指定目录的内容作为版本目录。
func TestInstallUsesWheelContentDirectory(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "uv-0.1.0-py3-none-any.whl")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string]string{"uv/__init__.py": "", "uv-0.1.0.data/scripts/uv": "binary", "uv-0.1.0.dist-info/WHEEL": ""} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "dist", "uv", "0.1.0")
	if err := install(archive, target, "uv-0.1.0.data/scripts"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 || entries[0].Name() != "uv" {
		t.Fatalf("版本目录内容不符合预期: %v %v", entries, err)
	}
}

// TestLockActiveSkipsVersionBeingRemoved 验证其他进程持有独占锁清理中的版本不会被选入命令环境。
func TestLockActiveSkipsVersionBeingRemoved(t *testing.T) {
	root := t.TempDir()
	manager := New(root, t.TempDir(), func() {})
	t.Cleanup(manager.Close)
	for _, version := range []string{"0.9.0", "0.10.0"} {
		if err := os.MkdirAll(filepath.Join(root, "dist", "uv", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	remover := flock.New(filepath.Join(root, "dist", "uv", "0.10.0.lock"))
	if locked, err := remover.TryLock(); err != nil || !locked {
		t.Fatalf("取得独占锁失败: %v", err)
	}
	t.Cleanup(func() { _ = remover.Unlock() })
	if version := manager.lockActive("uv"); version != "0.9.0" {
		t.Fatalf("应跳过清理中的版本，实际 %q", version)
	}
}

// zipArchive 生成包含指定文件的 zip 内容。
func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestUpdateInstallsLatestReleases 验证更新选取索引中最新的正式 uv 与最新的 Node.js LTS，并按索引给出的摘要校验后安装。
func TestUpdateInstallsLatestReleases(t *testing.T) {
	uvSuffix := strings.TrimPrefix(uvArtifacts[platform()].file, "uv-"+uvVersion+"-")
	uvFile := "uv-99.0.0-" + uvSuffix
	wheel := zipArchive(t, map[string]string{"uv-99.0.0.data/scripts/uv": "binary"})
	wheelSum := sha256.Sum256(wheel)
	nodeFile := "node-v98.0.0" + strings.TrimPrefix(nodeArtifacts[platform()].file, "node-v"+nodeVersion)
	var nodeArchive []byte
	if strings.HasSuffix(nodeFile, ".zip") {
		nodeArchive = zipArchive(t, map[string]string{"node-v98.0.0/node.exe": "binary"})
	} else {
		nodeArchive = buildTarGz(t, []tarEntry{{name: "node-v98.0.0/bin/node", content: "binary"}})
	}
	nodeSum := sha256.Sum256(nodeArchive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/uv/":
			_, _ = fmt.Fprintf(w, `<a href="/files/uv-100.0.0rc1-%s#sha256=00">pre</a><a href="/files/%s#sha256=%s">latest</a><a href="/files/uv-1.0.0-%s#sha256=11">old</a>`,
				uvSuffix, uvFile, hex.EncodeToString(wheelSum[:]), uvSuffix)
		case "/files/" + uvFile:
			_, _ = w.Write(wheel)
		case "/node/index.json":
			_, _ = w.Write([]byte(`[{"version":"v99.0.0","lts":false},{"version":"v98.0.0","lts":"Future"},{"version":"v24.0.0","lts":"Krypton"}]`))
		case "/node/v98.0.0/SHASUMS256.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(nodeSum[:]), nodeFile)
		case "/node/v98.0.0/" + nodeFile:
			_, _ = w.Write(nodeArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	manager := New(t.TempDir(), t.TempDir(), func() {})
	t.Cleanup(manager.Close)
	sources := Sources{PyPIIndexURL: server.URL + "/simple", NodeDownloadURL: server.URL + "/node"}
	if err := manager.updateUV(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	if err := manager.updateNode(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	if uv, node := manager.activeVersion("uv"), manager.activeVersion("node"); uv != "99.0.0" || node != "98.0.0" {
		t.Fatalf("更新后的版本不符合预期: uv=%s node=%s", uv, node)
	}
}

// TestDetectSourcesByRegion 验证中国大陆或无法探测时使用国内镜像，其他地区使用官方源。
func TestDetectSourcesByRegion(t *testing.T) {
	for country, expected := range map[string]Sources{"CN": chinaSources, "US": {}, "HK": {}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "fl=1\nip=1.2.3.4\nloc=%s\n", country)
		}))
		if sources, detected := detectSources(context.Background(), http.DefaultClient, server.URL); sources != expected || !detected {
			t.Fatalf("%s 的下载源不符合预期: %+v", country, sources)
		}
		server.Close()
	}
	if sources, detected := detectSources(context.Background(), http.DefaultClient, "http://127.0.0.1:1/unreachable"); sources != chinaSources || detected {
		t.Fatalf("无法探测时应使用国内镜像且标记为未探测: %+v", sources)
	}
}

// TestUninstallAndInstall 验证卸载删除运行环境与缓存、停止自动安装且允许领取运行，重新安装后恢复自动准备。
func TestUninstallAndInstall(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	root, cache := filepath.Join(t.TempDir(), "toolchains"), filepath.Join(t.TempDir(), "cache")
	manager := New(root, cache, func() {})
	t.Cleanup(manager.Close)
	manager.sources = &Sources{PyPIIndexURL: server.URL}
	for _, dir := range []string{filepath.Join(root, "dist", "uv", "0.1.0"), filepath.Join(cache, "uv")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manager.use("uv", "0.1.0")
	if err := manager.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if dirExists(root) || dirExists(cache) {
		t.Fatal("卸载后应删除运行环境与缓存")
	}
	if !manager.Ensure() || manager.Status().State != StateUninstalled || len(manager.Environment().PathPrefix) != 0 {
		t.Fatalf("卸载后应允许领取且不改动命令环境: %+v", manager.Status())
	}
	manager.mu.Lock()
	preparing := manager.preparing
	manager.mu.Unlock()
	if preparing {
		t.Fatal("卸载后不应自动安装")
	}
	if err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	if manager.Status().State == StateUninstalled {
		t.Fatal("重新安装后不应保持卸载状态")
	}
}

// TestUninstallFailureStaysRetryable 验证删除失败时不记录卸载，界面仍可再次卸载。
func TestUninstallFailureStaysRetryable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("目录权限在 Windows 上不阻止删除")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "toolchains")
	manager := New(root, filepath.Join(t.TempDir(), "cache"), func() {})
	t.Cleanup(manager.Close)
	locked := filepath.Join(root, "dist", "uv", "0.1.0")
	if err := os.MkdirAll(filepath.Join(locked, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 去掉目录写权限使其中的文件无法删除。
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := manager.Uninstall(); err == nil {
		t.Fatal("删除失败时应返回错误")
	}
	if manager.uninstalled() || manager.Status().State == StateUninstalled {
		t.Fatal("删除失败时不应记录卸载")
	}
}

// TestUpToDateComparesPythonVersion 验证默认 Python 低于内置版本时视为需要安装。
func TestUpToDateComparesPythonVersion(t *testing.T) {
	root := t.TempDir()
	manager := New(root, t.TempDir(), func() {})
	t.Cleanup(manager.Close)
	for _, name := range []string{"uv/" + uvVersion, "node/" + nodeVersion} {
		if err := os.MkdirAll(filepath.Join(root, "dist", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for version, expected := range map[string]bool{"3.13.1": false, PythonVersion: true, "3.13.99": true} {
		if err := os.WriteFile(filepath.Join(root, defaultPythonMarker), []byte(version), 0o644); err != nil {
			t.Fatal(err)
		}
		if manager.upToDate() != expected {
			t.Fatalf("Python %s 的判断不符合预期", version)
		}
	}
}

// TestUpdateRejectedWhileUninstalling 验证卸载进行中时更新返回 ErrBusy。
func TestUpdateRejectedWhileUninstalling(t *testing.T) {
	manager := New(t.TempDir(), t.TempDir(), func() {})
	t.Cleanup(manager.Close)
	manager.uninstalling = true
	if _, err := manager.Update(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("卸载中更新应返回 ErrBusy: %v", err)
	}
}
