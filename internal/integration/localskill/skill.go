// Package localskill 读取、安装与删除这台电脑上的 Agent Skills 技能。
//
// 技能是含 SKILL.md 的文件夹。助理安装的技能位于主目录下以品牌标识命名的隐藏目录下的 skills，同时只读使用
// ~/.agents/skills 与 ~/.claude/skills 中的技能，重名时按目录顺序取第一个。
package localskill

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// FileName 是技能说明文件的名称。
const FileName = "SKILL.md"

// Source 定义技能所在目录的来源。
type Source string

const (
	// SourceManaged 表示助理安装的技能，位于主目录下以品牌标识命名的隐藏目录。
	SourceManaged Source = "managed"
	// SourceAgents 表示跨工具共用的技能，位于 ~/.agents/skills。
	SourceAgents Source = "agents"
	// SourceClaude 表示 Claude 目录中的技能，位于 ~/.claude/skills。
	SourceClaude Source = "claude"
)

// Dir 是一个技能目录及其来源。
type Dir struct {
	Path   string
	Source Source
}

// Skill 是一个可用技能的名称、简介、所在文件夹与来源。
type Skill struct {
	Name        string
	Description string
	Dir         string // 技能文件夹的绝对路径。
	Source      Source
	Fork        bool // 元数据声明 context: fork，技能交给独立上下文的子 Agent 执行。
}

// DefaultDirs 按优先级返回用户主目录下的技能目录，第一个是助理安装技能的目录。
func DefaultDirs() ([]Dir, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []Dir{
		{Path: filepath.Join(home, "."+brand.Build().Slug, "skills"), Source: SourceManaged},
		{Path: filepath.Join(home, ".agents", "skills"), Source: SourceAgents},
		{Path: filepath.Join(home, ".claude", "skills"), Source: SourceClaude},
	}, nil
}

// frontMatter 是 SKILL.md 元数据中使用的字段。
type frontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Context     string `yaml:"context"`
}

// colonValuePattern 匹配值中含冒号且未加引号的顶层字段。
var colonValuePattern = regexp.MustCompile(`^([A-Za-z0-9_-]+):[ \t]+([^'"|>\s].*:\s.*)$`)

// parse 拆分 SKILL.md 的元数据与正文；值中含冒号未加引号导致解析失败时补引号后重试。
func parse(data []byte) (frontMatter, string, error) {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return frontMatter{}, "", errors.New("缺少以 --- 开头的元数据")
	}
	header, body, found := strings.Cut(text[len("---\n"):], "\n---")
	if !found {
		return frontMatter{}, "", errors.New("元数据缺少结束的 ---")
	}
	// 结束分隔线所在行的其余内容不属于正文。
	if _, rest, ok := strings.Cut(body, "\n"); ok {
		body = rest
	} else {
		body = ""
	}
	var meta frontMatter
	err := yaml.Unmarshal([]byte(header), &meta)
	if err != nil {
		lines := strings.Split(header, "\n")
		for i, line := range lines {
			if match := colonValuePattern.FindStringSubmatch(line); match != nil {
				lines[i] = match[1] + ": " + fmt.Sprintf("%q", match[2])
			}
		}
		if retryErr := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &meta); retryErr != nil {
			return frontMatter{}, "", fmt.Errorf("元数据无法解析: %w", err)
		}
	}
	return meta, strings.TrimSpace(body), nil
}

// read 读取技能文件夹：缺少简介或元数据无法解析时返回错误；缺少名称时使用文件夹名，名称与文件夹不一致时记录警告照常读取。
func read(dir string, source Source) (Skill, string, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return Skill{}, "", err
	}
	meta, body, err := parse(data)
	if err != nil {
		return Skill{}, "", err
	}
	meta.Name, meta.Description = strings.TrimSpace(meta.Name), strings.TrimSpace(meta.Description)
	if meta.Description == "" {
		return Skill{}, "", errors.New("缺少技能简介 description")
	}
	if meta.Name == "" {
		meta.Name = filepath.Base(dir)
	}
	if meta.Name != filepath.Base(dir) {
		slog.Warn("技能名称与所在文件夹不一致", "skill", meta.Name, "dir", dir)
	}
	return Skill{Name: meta.Name, Description: meta.Description, Dir: dir, Source: source, Fork: strings.TrimSpace(meta.Context) == "fork"}, body, nil
}

// scan 按目录优先级列出技能，重名时保留先出现的并记录警告，无法读取的技能跳过并记录原因；结果按名称排序。
func scan(dirs []Dir) []Skill {
	var skills []Skill
	seen := make(map[string]string)
	for _, root := range dirs {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("读取技能目录失败", "dir", root.Path, "error", err)
			}
			continue
		}
		for _, entry := range entries {
			dir := filepath.Join(root.Path, entry.Name())
			// 跳过隐藏项与不含 SKILL.md 的项，符号链接按其指向判断。
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if info, err := os.Stat(filepath.Join(dir, FileName)); err != nil || info.IsDir() {
				continue
			}
			skill, _, err := read(dir, root.Source)
			if err != nil {
				slog.Warn("跳过无法读取的技能", "dir", dir, "error", err)
				continue
			}
			if shadowing, exists := seen[skill.Name]; exists {
				slog.Warn("技能重名，使用优先级更高的技能", "skill", skill.Name, "used", shadowing, "ignored", dir)
				continue
			}
			seen[skill.Name] = dir
			skills = append(skills, skill)
		}
	}
	slices.SortFunc(skills, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return skills
}
