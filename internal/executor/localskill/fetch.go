package localskill

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/support/filex"
	"github.com/runforyou-ai/support/str"
)

// maxDiscoverDepth 是在技能容器目录下查找技能的最大层数，覆盖按分类分组的目录结构。
const maxDiscoverDepth = 3

// githubArchiveURL 是 GitHub 仓库压缩包的地址格式，参数依次为所有者、仓库名与分支。
var githubArchiveURL = "https://github.com/%s/%s/archive/%s.tar.gz"

// shorthandPattern 匹配 GitHub 简写 owner/repo，可跟仓库内路径。
var shorthandPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(/.+)?$`)

// containerDirs 是仓库中按惯例存放技能的目录，按查找顺序排列，空串表示来源根目录。
var containerDirs = []string{"skills", "skills/.curated", ".agents/skills", ".claude/skills", ""}

// fetch 把来源取到 staging 下，返回查找技能的起点目录。
func (s *Store) fetch(ctx context.Context, source, staging string) (string, error) {
	if source == "" {
		return "", errors.New("请提供技能来源")
	}
	local := source
	if rest, ok := strings.CutPrefix(source, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, `\`)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		local = filepath.Join(home, rest)
	}
	if filepath.IsAbs(local) {
		info, err := os.Stat(local)
		if err != nil {
			return "", fmt.Errorf("读取本机路径 %s 失败: %w", local, err)
		}
		target := filepath.Join(staging, "source")
		if info.IsDir() {
			return target, copyDir(local, target)
		}
		return target, unpack(local, target)
	}
	if parsed, err := url.Parse(source); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		// GitHub 上的发布附件与仓库压缩包地址按普通下载处理。
		archiveURL := strings.HasSuffix(parsed.Path, ".zip") || strings.HasSuffix(parsed.Path, ".tar.gz") || strings.HasSuffix(parsed.Path, ".tgz") ||
			str.Contains(parsed.Path, "/releases/download/", "/archive/")
		if (parsed.Host == "github.com" || parsed.Host == "www.github.com") && !archiveURL {
			return s.fetchGitHub(ctx, strings.Split(strings.Trim(parsed.Path, "/"), "/"), staging)
		}
		return s.fetchURL(ctx, source, staging)
	}
	if match := shorthandPattern.FindStringSubmatch(source); match != nil {
		segments := []string{match[1], match[2]}
		if match[3] != "" {
			segments = append(segments, "tree", "HEAD")
			segments = append(segments, strings.Split(strings.Trim(match[3], "/"), "/")...)
		}
		return s.fetchGitHub(ctx, segments, staging)
	}
	return "", fmt.Errorf("无法识别技能来源 %q：请提供 GitHub 地址、owner/repo、压缩包地址或本机路径", source)
}

// fetchGitHub 下载 GitHub 仓库压缩包，segments 为地址路径：owner/repo，可跟 tree|blob/<分支>/<仓库内路径>。
func (s *Store) fetchGitHub(ctx context.Context, segments []string, staging string) (string, error) {
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return "", errors.New("GitHub 地址需要包含仓库所有者与仓库名")
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	ref, sub := "HEAD", ""
	if len(segments) >= 4 && (segments[2] == "tree" || segments[2] == "blob") {
		ref, sub = segments[3], path.Join(segments[4:]...)
		// 指向 SKILL.md 文件时以其所在文件夹为技能。
		if path.Base(sub) == FileName {
			sub = path.Dir(sub)
		}
	}
	archivePath := filepath.Join(staging, "download")
	address := fmt.Sprintf(githubArchiveURL, url.PathEscape(owner), url.PathEscape(repo), ref)
	if err := s.download(ctx, address, archivePath); err != nil {
		return "", err
	}
	root := filepath.Join(staging, "source")
	if err := checkTarGz(archivePath); err != nil {
		return "", fmt.Errorf("解压仓库失败: %w", err)
	}
	if err := filex.ExtractTarGz(archivePath, root); err != nil {
		return "", fmt.Errorf("解压仓库失败: %w", err)
	}
	// 仓库压缩包的全部内容位于一个顶层目录中。
	if entries, err := os.ReadDir(root); err == nil && len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(root, entries[0].Name())
	}
	if sub == "" || sub == "." {
		return root, nil
	}
	dir, err := filex.EntryPath(root, sub)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("仓库中没有目录 %s", sub)
	}
	return dir, nil
}

// fetchURL 下载地址内容：压缩包解压后查找技能，SKILL.md 文件作为只含说明的技能。
func (s *Store) fetchURL(ctx context.Context, address, staging string) (string, error) {
	file := filepath.Join(staging, "download")
	if err := s.download(ctx, address, file); err != nil {
		return "", err
	}
	return filepath.Join(staging, "source"), unpack(file, filepath.Join(staging, "source"))
}

// unpack 按文件内容解压 zip 或 tar.gz；内容是 SKILL.md 时放入以其名称命名的文件夹。
func unpack(file, target string) error {
	// 读取文件开头 4 字节判断压缩格式。
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	header := make([]byte, 4)
	n, err := io.ReadFull(input, header)
	input.Close()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	switch header = header[:n]; {
	case bytes.HasPrefix(header, []byte("PK\x03\x04")):
		if err := checkZip(file); err != nil {
			return err
		}
		return filex.ExtractZip(file, target)
	case bytes.HasPrefix(header, []byte{0x1f, 0x8b}):
		if err := checkTarGz(file); err != nil {
			return err
		}
		return filex.ExtractTarGz(file, target)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	meta, _, err := parse(data)
	if err != nil {
		return errors.New("来源不是 zip、tar.gz 压缩包或 SKILL.md 文件")
	}
	name := cmp.Or(strings.TrimSpace(meta.Name), "skill")
	dir, err := filex.EntryPath(target, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), data, 0o644)
}

// download 把地址内容写入 file。
func (s *Store) download(ctx context.Context, address, file string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", brand.Build().Slug)
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", address, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败：HTTP %d", address, response.StatusCode)
	}
	output, err := os.Create(file)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(response.Body, maxDownloadBytes+1))
	if copyErr == nil && written > maxDownloadBytes {
		copyErr = fmt.Errorf("文件超过 %d MiB 上限", maxDownloadBytes>>20)
	}
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return fmt.Errorf("下载 %s 失败: %w", address, err)
	}
	return nil
}

// discover 查找 root 中的技能：root 本身含 SKILL.md 时只返回它，否则在惯例容器目录中向下查找，不进入已识别的技能文件夹。
func discover(root string) []Skill {
	if skill, _, err := read(root, SourceManaged); err == nil {
		return []Skill{skill}
	} else if !errors.Is(err, fs.ErrNotExist) {
		slog.WarnContext(context.Background(), "跳过无法读取的技能", "dir", root, "error", err)
	}
	var found []Skill
	visited := make(map[string]bool)
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil || depth > maxDiscoverDepth {
			return
		}
		for _, entry := range entries {
			child := filepath.Join(dir, entry.Name())
			if !entry.IsDir() || visited[child] || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			visited[child] = true
			skill, _, err := read(child, SourceManaged)
			switch {
			case err == nil:
				found = append(found, skill)
			case errors.Is(err, fs.ErrNotExist):
				walk(child, depth+1)
			default:
				slog.WarnContext(context.Background(), "跳过无法读取的技能", "dir", child, "error", err)
			}
		}
	}
	for _, container := range containerDirs {
		walk(filepath.Join(root, filepath.FromSlash(container)), 1)
	}
	return found
}

// copyDir 复制文件夹中的目录与普通文件，跳过 .git；指向文件夹内文件的符号链接复制为其内容，其余符号链接跳过。
func copyDir(source, target string) error {
	realSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		switch {
		case entry.IsDir() && entry.Name() == ".git":
			return filepath.SkipDir
		case entry.IsDir():
			return os.MkdirAll(destination, 0o755)
		case entry.Type()&fs.ModeSymlink != 0 && !resolvesWithin(realSource, current):
			slog.WarnContext(context.Background(), "跳过指向技能文件夹以外的符号链接", "path", current)
			return nil
		}
		info, err := os.Stat(current)
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		input, err := os.Open(current)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o600)
		if err != nil {
			return errors.Join(err, input.Close())
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, output.Close(), input.Close())
	})
}

// dropEscapingLinks 删除技能文件夹中不能解析到该文件夹内已有位置的符号链接。
func dropEscapingLinks(dir string) error {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSymlink == 0 || resolvesWithin(realDir, current) {
			return err
		}
		slog.WarnContext(context.Background(), "丢弃指向技能文件夹以外的符号链接", "path", current)
		return os.Remove(current)
	})
}

// resolvesWithin 判断 path 经符号链接解析后是否是 realDir 内已存在的位置，realDir 须是已解析的实际路径。
func resolvesWithin(realDir, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && strings.HasPrefix(resolved, realDir+string(filepath.Separator))
}
