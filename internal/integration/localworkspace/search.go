package localworkspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
)

// maxSearchFileBytes 是内容搜索时单个文件的字节上限，超出的文件跳过。
const maxSearchFileBytes = 2 << 20

// skippedDirs 是遍历目录时跳过的目录名。
var skippedDirs = []string{".git"}

// GlobInfo 在搜索起点目录下按 glob 模式匹配文件，模式相对起点目录匹配，绝对路径模式从其固定前缀目录开始匹配；结果按修改时间从新到旧排列。
func (b *Backend) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	pattern, base := req.Pattern, req.Path
	// ~ 开头的模式按用户主目录展开后当作绝对路径模式。
	if pattern == "~" || strings.HasPrefix(pattern, "~/") || strings.HasPrefix(pattern, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.New("无法确定用户主目录")
		}
		pattern = filepath.Join(home, pattern[1:])
	}
	if filepath.IsAbs(pattern) {
		base, pattern = doublestar.SplitPattern(filepath.ToSlash(pattern))
		base = filepath.FromSlash(base)
	}
	if !doublestar.ValidatePattern(pattern) {
		return nil, fmt.Errorf("glob 模式无效：%s", req.Pattern)
	}
	dir, err := b.resolve(base)
	if err != nil {
		return nil, err
	}
	var infos []filesystem.FileInfo
	err = b.walk(ctx, dir, func(real string, entry fs.DirEntry) error {
		rel, err := filepath.Rel(dir, real)
		if err != nil || !doublestar.MatchUnvalidated(pattern, filepath.ToSlash(rel)) {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			infos = append(infos, fileInfo(real, info))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 按修改时间从新到旧排列，时间相同时按路径排列。
	slices.SortStableFunc(infos, func(left, right filesystem.FileInfo) int {
		if order := strings.Compare(right.ModifiedAt, left.ModifiedAt); order != 0 {
			return order
		}
		return strings.Compare(left.Path, right.Path)
	})
	return infos, nil
}

// GrepRaw 在搜索路径下的文本文件中按 Go 正则表达式搜索，返回匹配行及所需的上下文行；跳过二进制文件与超过字节上限的文件。
func (b *Backend) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, errors.New("搜索模式不能为空")
	}
	flags := ""
	if req.CaseInsensitive {
		flags += "i"
	}
	if req.EnableMultiline {
		flags += "s"
	}
	expression := req.Pattern
	if flags != "" {
		expression = "(?" + flags + ")" + expression
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("正则表达式无效：%v", err)
	}
	if req.Glob != "" && !doublestar.ValidatePattern(req.Glob) {
		return nil, fmt.Errorf("glob 模式无效：%s", req.Glob)
	}
	start, err := b.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	var matches []filesystem.GrepMatch
	err = b.walk(ctx, start, func(real string, entry fs.DirEntry) error {
		// 过滤路径相对搜索目录计算，搜索单个文件时取文件名。
		rel := filepath.Base(real)
		if real != start {
			relative, err := filepath.Rel(start, real)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(relative)
		}
		// 不含 / 的 glob 按文件名匹配，含 / 的按相对搜索路径匹配；type 按扩展名过滤。
		if req.Glob != "" && !doublestar.MatchUnvalidated(req.Glob, rel) && (strings.Contains(req.Glob, "/") || !doublestar.MatchUnvalidated(req.Glob, path.Base(rel))) {
			return nil
		}
		if req.FileType != "" && strings.TrimPrefix(path.Ext(rel), ".") != req.FileType {
			return nil
		}
		matches = append(matches, b.grepFile(real, re, req)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// grepFile 搜索单个普通文本文件，多行模式在全文上匹配并报告匹配覆盖的全部行。
func (b *Backend) grepFile(real string, re *regexp.Regexp, req *filesystem.GrepRequest) []filesystem.GrepMatch {
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return nil
	}
	content, err := os.ReadFile(real)
	if err != nil || isBinary(content) {
		return nil
	}
	text := string(content)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	hits := make([]bool, len(lines))
	found := false
	if req.EnableMultiline {
		// 跨行匹配覆盖的每一行都作为匹配行输出。
		for _, bounds := range re.FindAllStringIndex(text, -1) {
			first := strings.Count(text[:bounds[0]], "\n")
			last := first + strings.Count(text[bounds[0]:bounds[1]], "\n")
			for line := first; line <= min(last, len(lines)-1); line++ {
				hits[line], found = true, true
			}
		}
	} else {
		for index, line := range lines {
			if re.MatchString(line) {
				hits[index], found = true, true
			}
		}
	}
	if !found {
		return nil
	}
	// 匹配行连同前后上下文行按行号顺序输出，重叠的上下文只输出一次。
	shown := make([]bool, len(lines))
	for index, hit := range hits {
		if hit {
			for line := max(0, index-req.BeforeLines); line <= min(len(lines)-1, index+req.AfterLines); line++ {
				shown[line] = true
			}
		}
	}
	var matches []filesystem.GrepMatch
	for index, show := range shown {
		if show {
			matches = append(matches, filesystem.GrepMatch{Path: real, Line: index + 1, Content: lines[index]})
		}
	}
	return matches
}

// walk 遍历起点下的普通文件，起点是文件时只访问该文件；起点是目录链接时遍历其目标并按起点路径报告，跳过版本库目录，不跟随起点以下的符号链接目录。
func (b *Backend) walk(ctx context.Context, start string, visit func(real string, entry fs.DirEntry) error) error {
	info, err := os.Stat(start)
	if err != nil {
		return fmt.Errorf("无法访问：%s", start)
	}
	if !info.IsDir() {
		return visit(start, fs.FileInfoToDirEntry(info))
	}
	target, err := filepath.EvalSymlinks(start)
	if err != nil {
		return fmt.Errorf("无法访问：%s", start)
	}
	return filepath.WalkDir(target, func(walked string, entry fs.DirEntry, err error) error {
		real := walked
		if rel, relErr := filepath.Rel(target, walked); relErr == nil {
			real = filepath.Join(start, rel)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if walked != target && slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(walked)
			if err != nil || info.IsDir() {
				return nil
			}
			return visit(real, fs.FileInfoToDirEntry(info))
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return visit(real, entry)
	})
}
