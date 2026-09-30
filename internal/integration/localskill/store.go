package localskill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// downloadTimeout 是下载一个技能来源的总时限。
const downloadTimeout = 5 * time.Minute

// ErrNotFound 表示没有对应名称的技能。
var ErrNotFound = errors.New("skill not found")

// Store 读取各技能目录中的技能，并在第一个目录中安装与删除技能，变更后调用 onChange。
type Store struct {
	dirs     []Dir
	client   *http.Client
	onChange func()
	mu       sync.Mutex
}

// NewStore 创建按 dirs 优先级读取技能的存储，dirs 的第一个目录是安装目录。
func NewStore(dirs []Dir, onChange func()) *Store {
	return &Store{dirs: dirs, client: &http.Client{Timeout: downloadTimeout}, onChange: onChange}
}

// List 按名称顺序返回全部可用技能。
func (s *Store) List(context.Context) ([]Skill, error) {
	return scan(s.dirs), nil
}

// Load 返回技能及其 SKILL.md 正文。
func (s *Store) Load(_ context.Context, name string) (Skill, string, error) {
	for _, skill := range scan(s.dirs) {
		if skill.Name == name {
			_, body, err := read(skill.Dir, skill.Source)
			return skill, body, err
		}
	}
	return Skill{}, "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Install 从来源取得技能并安装到安装目录，同名技能被替换：来源含多个技能时按 name 选择其中一个。
// 来源可以是 GitHub 简写 owner/repo[/路径]、GitHub 仓库或目录地址、压缩包或 SKILL.md 地址、本机文件夹或压缩包路径。
// 技能中解析到技能文件夹以外的符号链接被丢弃。
func (s *Store) Install(ctx context.Context, source, name string) (Skill, error) {
	installDir := s.dirs[0].Path
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("创建技能目录失败: %w", err)
	}
	// 暂存目录与安装目录位于同一上级目录，安装时整体改名。
	staging, err := os.MkdirTemp(filepath.Dir(installDir), ".skill-install-")
	if err != nil {
		return Skill{}, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(staging)
	root, err := s.fetch(ctx, strings.TrimSpace(source), staging)
	if err != nil {
		return Skill{}, err
	}
	skill, err := choose(discover(root), name)
	if err != nil {
		return Skill{}, err
	}
	if strings.HasPrefix(skill.Name, ".") || strings.ContainsAny(skill.Name, `/\:`) {
		return Skill{}, fmt.Errorf("技能名称 %q 不能作为文件夹名", skill.Name)
	}
	if err := dropEscapingLinks(skill.Dir); err != nil {
		return Skill{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 同名技能先移到暂存目录，新技能就位后随暂存目录删除，就位失败时移回。
	target := filepath.Join(installDir, skill.Name)
	previous := filepath.Join(staging, "previous")
	if err := os.Rename(target, previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Skill{}, fmt.Errorf("替换已安装的同名技能失败: %w", err)
	}
	if err := os.Rename(skill.Dir, target); err != nil {
		_ = os.Rename(previous, target)
		return Skill{}, fmt.Errorf("安装技能失败: %w", err)
	}
	skill.Dir, skill.Source = target, s.dirs[0].Source
	s.onChange()
	return skill, nil
}

// Remove 删除安装目录中对应名称的技能，返回技能是否存在；其他目录中的技能不删除。
func (s *Store) Remove(_ context.Context, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, skill := range scan(s.dirs[:1]) {
		if skill.Name != name {
			continue
		}
		if err := os.RemoveAll(skill.Dir); err != nil {
			return false, err
		}
		s.onChange()
		return true, nil
	}
	return false, nil
}

// choose 从来源中的技能里选择要安装的一个：只有一个时直接选择，多个时按名称或文件夹名选择。
func choose(found []Skill, name string) (Skill, error) {
	name = strings.TrimSpace(name)
	if len(found) == 0 {
		return Skill{}, errors.New("来源中没有找到含 SKILL.md 且带有简介的技能")
	}
	if name == "" {
		if len(found) == 1 {
			return found[0], nil
		}
		return Skill{}, fmt.Errorf("来源包含多个技能，请用 skill 参数指定其中一个：%s", skillNames(found))
	}
	for _, skill := range found {
		if skill.Name == name || filepath.Base(skill.Dir) == name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("来源中没有名为 %s 的技能，可选：%s", name, skillNames(found))
}

// skillNames 以顿号连接技能名称。
func skillNames(skills []Skill) string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return strings.Join(names, "、")
}
