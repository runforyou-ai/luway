// Package localworkspace 提供本机文件的读写与命令执行，相对路径与命令工作目录以会话默认文件夹为起点，模型看到的是本机绝对路径。
package localworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/support/filex"
)

const (
	// maxSymlinkDepth 是写入与限定访问范围时逐级解析符号链接的层数上限。
	maxSymlinkDepth = 40
)

// fileLocks 按本机绝对路径串行化同一文件的写入与修改，同一进程内的并行操作不互相覆盖。
var fileLocks sync.Map

// Workspace 读写本机文件并执行命令：绝对路径直接访问，~ 开头按用户主目录展开，相对路径以默认文件夹为起点。
type Workspace struct {
	root string
	// environment 叠加在命令的基础环境变量上。
	environment Environment
}

// New 以默认文件夹为相对路径起点打开本机文件访问，命令执行时在基础环境变量上叠加 environment。
func New(dir string, environment Environment) *Workspace {
	return &Workspace{root: filepath.Clean(dir), environment: environment}
}

// FileVersion 是读取或写入后文件的绝对路径与内容摘要。
type FileVersion struct {
	Path string
	Hash string
}

// TextRead 是一次按行读取的结果：以 cat -n 格式带行号的内容与文件版本。
type TextRead struct {
	FileVersion
	Content string
}

// Resolve 把模型给出的路径解析为本机绝对路径。
func (w *Workspace) Resolve(name string) (string, error) {
	switch {
	case name == "~" || strings.HasPrefix(name, "~/") || strings.HasPrefix(name, `~\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("无法确定用户主目录")
		}
		name = filepath.Join(home, name[1:])
	case !filepath.IsAbs(name):
		name = filepath.Join(w.root, name)
	}
	return filepath.Clean(name), nil
}

// Confine 确认模型给出的路径在展开已存在部分的符号链接后位于 dirs 中某个文件夹之内，不在时返回交给模型的错误。
func (w *Workspace) Confine(name string, dirs ...string) error {
	file, err := w.Resolve(name)
	if err != nil {
		return err
	}
	resolved, ok := realPath(file, maxSymlinkDepth)
	for _, dir := range dirs {
		root, rootOK := realPath(filepath.Clean(dir), maxSymlinkDepth)
		if ok && rootOK && (resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator))) {
			return nil
		}
	}
	return fmt.Errorf("不能访问 %s：只能访问本会话的文件夹 %s", file, w.root)
}

// realPath 按系统解析路径的方式逐级展开绝对路径中的符号链接，目标尚不存在的符号链接按其目标继续解析，不存在的部分按原样拼接；
// 符号链接超过 depth 层或无法读取时返回 false。
func realPath(path string, depth int) (string, bool) {
	return resolveLinks("", path, &depth)
}

// resolveLinks 以已展开的文件夹 base 为起点逐级解析 name，name 为绝对路径时从其根目录开始，depth 是剩余可展开的符号链接层数。
func resolveLinks(base, name string, depth *int) (string, bool) {
	if filepath.IsAbs(name) {
		volume := filepath.VolumeName(name)
		base, name = volume+string(filepath.Separator), name[len(volume):]
	}
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == filepath.Separator })
	for _, part := range parts {
		switch part {
		case ".":
			continue
		case "..":
			base = filepath.Dir(base)
			continue
		}
		next := filepath.Join(base, part)
		info, err := os.Lstat(next)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			base = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil || *depth == 0 {
			return "", false
		}
		*depth--
		resolved, ok := resolveLinks(base, target, depth)
		if !ok {
			return "", false
		}
		base = resolved
	}
	return base, true
}

// ReadText 按行读取文本文件，offset 从 1 开始，limit 为 0 时最多读取 2000 行；二进制文件与超出上限的文件返回错误，摘要按全文计算。
func (w *Workspace) ReadText(name string, offset, limit int) (TextRead, error) {
	file, err := w.Resolve(name)
	if err != nil {
		return TextRead{}, err
	}
	content, err := readFile(file, textfile.MaxReadBytes)
	if err != nil {
		return TextRead{}, err
	}
	if filex.IsBinary(content) {
		return TextRead{}, fmt.Errorf("二进制文件无法按文本读取：%s", file)
	}
	return TextRead{FileVersion: FileVersion{Path: file, Hash: textfile.Hash(content)}, Content: textfile.Numbered(content, offset, limit)}, nil
}

// WriteText 以完整内容创建或覆盖文件，缺少的上级目录一并创建；文件内容已与目标一致时直接返回，覆盖其他内容时要求 baseHash 与当前内容一致，指向符号链接时写入链接目标。
func (w *Workspace) WriteText(ctx context.Context, name, content, baseHash string) (FileVersion, error) {
	file, err := w.Resolve(name)
	if err != nil {
		return FileVersion{}, err
	}
	unlock := lockFile(file)
	defer unlock()
	// 排队期间操作已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return FileVersion{}, err
	}
	if current, err := os.ReadFile(file); err == nil {
		// 重复执行同一写入时文件已是目标内容，结果与首次写入相同。
		if bytes.Equal(current, []byte(content)) {
			return FileVersion{Path: file, Hash: textfile.Hash(current)}, nil
		}
		if err := textfile.CheckBase(file, current, baseHash); err != nil {
			return FileVersion{}, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return FileVersion{}, fileError("读取文件", file, err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return FileVersion{}, fileError("创建目录", filepath.Dir(file), err)
	}
	if err := replaceFile(file, []byte(content)); err != nil {
		return FileVersion{}, err
	}
	return FileVersion{Path: file, Hash: textfile.Hash([]byte(content))}, nil
}

// EditText 把文本文件中的原文替换为新内容；要求 baseHash 与当前内容一致，原文必须存在，未要求全部替换时必须唯一。
func (w *Workspace) EditText(ctx context.Context, name, oldString, newString string, replaceAll bool, baseHash string) (FileVersion, error) {
	file, err := w.Resolve(name)
	if err != nil {
		return FileVersion{}, err
	}
	unlock := lockFile(file)
	defer unlock()
	// 排队期间操作已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return FileVersion{}, err
	}
	content, err := readFile(file, textfile.MaxReadBytes)
	if err != nil {
		return FileVersion{}, err
	}
	if err := textfile.CheckBase(file, content, baseHash); err != nil {
		return FileVersion{}, err
	}
	if filex.IsBinary(content) {
		return FileVersion{}, fmt.Errorf("二进制文件无法按文本修改：%s", file)
	}
	text, err := textfile.Replace(file, string(content), oldString, newString, replaceAll)
	if err != nil {
		return FileVersion{}, err
	}
	if err := replaceFile(file, []byte(text)); err != nil {
		return FileVersion{}, err
	}
	return FileVersion{Path: file, Hash: textfile.Hash([]byte(text))}, nil
}

// lockFile 取得同一文件的进程内写锁，返回释放函数。
func lockFile(file string) func() {
	value, _ := fileLocks.LoadOrStore(file, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// readFile 读取普通文件，目录、管道与设备等非普通文件或超出字节上限时返回错误。
func readFile(file string, limit int64) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, fileError("读取文件", file, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("路径是目录，请用命令查看目录内容：%s", file)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("不是普通文件，无法读取：%s", file)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("文件超过 %d MB，无法读取：%s", limit>>20, file)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fileError("读取文件", file, err)
	}
	return content, nil
}

// replaceFile 先写入同目录的临时文件再替换目标，写入中途失败时原文件不变；已有文件保留原权限，新文件权限为 0644。
func replaceFile(file string, content []byte) error {
	// 符号链接按与限定访问范围相同的方式逐级解析到最终目标，目标尚不存在时同样写入目标，链接本身保留。
	resolved, ok := realPath(file, maxSymlinkDepth)
	if !ok {
		return fmt.Errorf("符号链接无法解析或层数过多：%s", file)
	}
	file = resolved
	mode := os.FileMode(0o644)
	if info, err := os.Stat(file); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不是普通文件，无法写入：%s", file)
		}
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".tmp-*")
	if err != nil {
		return fileError("写入文件", file, err)
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(content)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return fileError("写入文件", file, err)
	}
	if err := os.Chmod(temp.Name(), mode); err != nil {
		return fileError("写入文件", file, err)
	}
	if err := os.Rename(temp.Name(), file); err != nil {
		return fileError("写入文件", file, err)
	}
	return nil
}

// fileError 返回带操作、路径和系统原因的文件错误，系统原因取自 os 错误包装的底层错误。
func fileError(action, file string, err error) error {
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	switch {
	case errors.As(err, &pathErr):
		err = pathErr.Err
	case errors.As(err, &linkErr):
		err = linkErr.Err
	}
	return fmt.Errorf("无法%s：%s（%w）", action, file, err)
}
