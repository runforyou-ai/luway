// Package localworkspace 提供本机文件的读写与命令执行，相对路径与命令工作目录以会话默认文件夹为起点，模型看到的是本机绝对路径。
package localworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// maxReadBytes 是按文本读取单个文件的字节上限。
	maxReadBytes = 10 << 20
	// defaultReadLines 是未指定行数时读取的行数上限。
	defaultReadLines = 2000
	// binarySniffBytes 是判断二进制文件时检查的文件开头字节数。
	binarySniffBytes = 8000
	// maxSymlinkDepth 是写入时逐级解析符号链接的层数上限。
	maxSymlinkDepth = 40
)

// fileLocks 按本机绝对路径串行化同一文件的写入与修改，同一进程内的并行操作不互相覆盖。
var fileLocks sync.Map

// Workspace 读写本机文件并执行命令：绝对路径直接访问，~ 开头按用户主目录展开，相对路径以默认文件夹为起点。
type Workspace struct {
	root string
	// environment 叠加在命令的基础环境变量上。
	environment Environment
	// timeout 是单次命令的执行预算。
	timeout time.Duration
}

// New 以默认文件夹为相对路径起点打开本机文件访问，命令执行时在基础环境变量上叠加 environment。
func New(dir string, environment Environment) *Workspace {
	return &Workspace{root: filepath.Clean(dir), environment: environment, timeout: CommandTimeout}
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

// ReadText 按行读取文本文件，offset 从 1 开始，limit 为 0 时最多读取 2000 行；二进制文件与超出上限的文件返回错误，摘要按全文计算。
func (w *Workspace) ReadText(name string, offset, limit int) (TextRead, error) {
	file, err := w.Resolve(name)
	if err != nil {
		return TextRead{}, err
	}
	content, err := readFile(file, maxReadBytes)
	if err != nil {
		return TextRead{}, err
	}
	if isBinary(content) {
		return TextRead{}, fmt.Errorf("二进制文件无法按文本读取：%s", file)
	}
	result := TextRead{FileVersion: FileVersion{Path: file, Hash: contentHash(content)}}
	lines := strings.SplitAfter(string(content), "\n")
	start := max(offset, 1) - 1
	if limit <= 0 {
		limit = defaultReadLines
	}
	if len(content) == 0 || start >= len(lines) {
		result.Content = fmt.Sprintf("没有读到内容：文件为空，或第 %d 行超过了文件末尾。", start+1)
		return result, nil
	}
	end := min(len(lines), start+limit)
	var text strings.Builder
	for index, line := range lines[start:end] {
		line = strings.TrimSuffix(line, "\n")
		if index > 0 {
			text.WriteByte('\n')
		}
		fmt.Fprintf(&text, "%6d\t%s", start+index+1, line)
	}
	result.Content = text.String()
	return result, nil
}

// WriteText 以完整内容创建或覆盖文件，缺少的上级目录一并创建；覆盖已有文件时要求 baseHash 与当前内容一致，指向符号链接时写入链接目标。
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
		if err := checkBase(file, current, baseHash); err != nil {
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
	return FileVersion{Path: file, Hash: contentHash([]byte(content))}, nil
}

// EditText 把文本文件中的原文替换为新内容；要求 baseHash 与当前内容一致，原文必须存在，未要求全部替换时必须唯一。
func (w *Workspace) EditText(ctx context.Context, name, oldString, newString string, replaceAll bool, baseHash string) (FileVersion, error) {
	file, err := w.Resolve(name)
	if err != nil {
		return FileVersion{}, err
	}
	if oldString == "" {
		return FileVersion{}, errors.New("要替换的原文不能为空")
	}
	if oldString == newString {
		return FileVersion{}, errors.New("替换内容与原文相同")
	}
	unlock := lockFile(file)
	defer unlock()
	// 排队期间操作已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return FileVersion{}, err
	}
	content, err := readFile(file, maxReadBytes)
	if err != nil {
		return FileVersion{}, err
	}
	if err := checkBase(file, content, baseHash); err != nil {
		return FileVersion{}, err
	}
	if isBinary(content) {
		return FileVersion{}, fmt.Errorf("二进制文件无法按文本修改：%s", file)
	}
	text := string(content)
	count := strings.Count(text, oldString)
	switch {
	case count == 0:
		return FileVersion{}, fmt.Errorf("文件中找不到要替换的原文：%s", file)
	case count > 1 && !replaceAll:
		return FileVersion{}, fmt.Errorf("原文在文件中出现 %d 次，请提供更多上下文使其唯一，或设置 replace_all：%s", count, file)
	}
	if replaceAll {
		text = strings.ReplaceAll(text, oldString, newString)
	} else {
		text = strings.Replace(text, oldString, newString, 1)
	}
	if err := replaceFile(file, []byte(text)); err != nil {
		return FileVersion{}, err
	}
	return FileVersion{Path: file, Hash: contentHash([]byte(text))}, nil
}

// checkBase 校验已有文件的当前内容与读取时的摘要一致：没有读取记录或内容已变化时返回要求重新读取的错误。
func checkBase(file string, current []byte, baseHash string) error {
	if baseHash == "" {
		return fmt.Errorf("文件已存在，覆盖或修改前先用 read_file 读取：%s", file)
	}
	if contentHash(current) != baseHash {
		return fmt.Errorf("文件在读取后已被改动，请重新用 read_file 读取后再改：%s", file)
	}
	return nil
}

// lockFile 取得同一文件的进程内写锁，返回释放函数。
func lockFile(file string) func() {
	value, _ := fileLocks.LoadOrStore(file, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// contentHash 返回文件内容的 SHA-256 摘要。
func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
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

// isBinary 按文件开头是否包含空字节判断二进制文件。
func isBinary(content []byte) bool {
	return bytes.IndexByte(content[:min(len(content), binarySniffBytes)], 0) >= 0
}

// replaceFile 先写入同目录的临时文件再替换目标，写入中途失败时原文件不变；已有文件保留原权限，新文件权限为 0644。
func replaceFile(file string, content []byte) error {
	// 符号链接逐级解析到最终目标，目标尚不存在时同样写入目标，链接本身保留；
	// 每一级先按文件系统解析所在目录，链接中的 .. 相对真实目录计算。
	for range maxSymlinkDepth {
		if dir, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
			file = filepath.Join(dir, filepath.Base(file))
		}
		info, err := os.Lstat(file)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(file)
		if err != nil {
			return fileError("解析符号链接", file, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(file), target)
		}
		file = filepath.Clean(target)
	}
	if info, err := os.Lstat(file); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("符号链接层数过多：%s", file)
	}
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
