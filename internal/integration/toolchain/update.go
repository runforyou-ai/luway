package toolchain

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/mod/semver"
)

// Update 把 uv 与 Node.js 更新到下载源的最新稳定版本，并把默认 Python 升级到同系列的最新补丁版本；返回版本是否有变化。
// uv 取 PyPI 索引中最新的正式版本并按索引给出的 SHA256 校验，Node.js 取版本索引中最新的 LTS 版本并按下载源的 SHASUMS256.txt 校验；
// 更新的校验值与安装包来自同一下载源，首次安装按内置 SHA256 校验。
// 运行环境尚未完成首次准备时返回 ErrNotReady，正在准备、更新或卸载时返回 ErrBusy。
func (m *Manager) Update(ctx context.Context) (bool, error) {
	m.mu.Lock()
	switch {
	case m.preparing || m.updating || m.uninstalling:
		m.mu.Unlock()
		return false, ErrBusy
	case !m.usable():
		m.mu.Unlock()
		return false, ErrNotReady
	}
	m.updating = true
	m.wg.Add(1)
	m.mu.Unlock()
	// 管理器关闭时一并取消更新。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	defer func() {
		m.mu.Lock()
		m.updating = false
		m.mu.Unlock()
		m.wg.Done()
		m.onChange()
	}()
	m.onChange()
	sources := m.downloadSources(ctx)
	before := m.Info()
	if err := m.updateUV(ctx, sources); err != nil {
		return false, err
	}
	if err := m.updateNode(ctx, sources); err != nil {
		return false, err
	}
	if err := m.installPython(ctx, sources, pythonSeries, true); err != nil {
		return false, err
	}
	m.removeStale()
	after := m.Info()
	slog.Info("Agent 运行环境已更新", "before", before, "after", after)
	return after != before, nil
}

// updateUV 在 PyPI 索引列出更高的正式版本时安装该版本。
func (m *Manager) updateUV(ctx context.Context, sources Sources) error {
	baseline := uvArtifacts[platform()]
	// 同一平台各版本 wheel 的文件名只有版本号不同。
	suffix := strings.TrimPrefix(baseline.file, "uv-"+uvVersion+"-")
	links, err := indexLinks(ctx, m.client, cmp.Or(sources.PyPIIndexURL, defaultPyPIIndexURL), "uv")
	if err != nil {
		return err
	}
	var latest indexLink
	latestVersion := ""
	for _, link := range links {
		version, ok := strings.CutSuffix(strings.TrimPrefix(link.file, "uv-"), "-"+suffix)
		if !ok || !strings.HasPrefix(link.file, "uv-") || !semver.IsValid("v"+version) || semver.Prerelease("v"+version) != "" {
			continue
		}
		if latestVersion == "" || semver.Compare("v"+version, "v"+latestVersion) > 0 {
			latest, latestVersion = link, version
		}
	}
	if latestVersion == "" || atLeast(m.activeVersion("uv"), latestVersion) {
		return nil
	}
	if latest.sha256 == "" {
		return &stepError{failure: FailureVerify, err: fmt.Errorf("package index gives no SHA256 for %s", latest.file)}
	}
	resolve := func(context.Context) (string, error) { return latest.url, nil }
	return m.installDist(ctx, "uv", latestVersion, resolve, artifact{file: latest.file, sha256: latest.sha256}, uvWheelContent(latestVersion))
}

// updateNode 在版本索引列出更高的 LTS 版本时安装该版本。
func (m *Manager) updateNode(ctx context.Context, sources Sources) error {
	base := cmp.Or(sources.NodeDownloadURL, defaultNodeDownloadURL)
	indexCtx, cancel := context.WithTimeoutCause(ctx, downloadStallTimeout, errDownloadStalled)
	defer cancel()
	response, err := get(indexCtx, m.client, base+"/index.json", "application/json")
	if err != nil {
		return err
	}
	var releases []struct {
		Version string          `json:"version"`
		LTS     json.RawMessage `json:"lts"`
	}
	err = json.NewDecoder(response.Body).Decode(&releases)
	response.Body.Close()
	if err != nil {
		return &stepError{failure: FailureDownload, err: fmt.Errorf("decode Node.js release index: %w", err)}
	}
	latest := ""
	for _, release := range releases {
		// lts 为 false 表示非 LTS 版本，LTS 版本为代号字符串。
		if string(release.LTS) != "false" && semver.IsValid(release.Version) && (latest == "" || semver.Compare(release.Version, "v"+latest) > 0) {
			latest = strings.TrimPrefix(release.Version, "v")
		}
	}
	if latest == "" || atLeast(m.activeVersion("node"), latest) {
		return nil
	}
	// 同一平台各版本发行物的文件名只有版本号不同。
	file := "node-v" + latest + strings.TrimPrefix(nodeArtifacts[platform()].file, "node-v"+nodeVersion)
	digest, err := nodeChecksum(ctx, m.client, base, latest, file)
	if err != nil {
		return err
	}
	resolve := func(context.Context) (string, error) { return nodeFileURL(sources, latest, file), nil }
	return m.installDist(ctx, "node", latest, resolve, artifact{file: file, sha256: digest}, "")
}

// nodeChecksum 从指定版本的 SHASUMS256.txt 中读取发行物的 SHA256。
func nodeChecksum(ctx context.Context, client *http.Client, base, version, file string) (string, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, downloadStallTimeout, errDownloadStalled)
	defer cancel()
	response, err := get(ctx, client, base+"/v"+version+"/SHASUMS256.txt", "text/plain")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if digest, name, ok := strings.Cut(scanner.Text(), "  "); ok && name == file {
			return digest, nil
		}
	}
	return "", &stepError{failure: FailureVerify, err: fmt.Errorf("SHASUMS256.txt of Node.js %s does not list %s", version, file)}
}
