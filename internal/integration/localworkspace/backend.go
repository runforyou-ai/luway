// Package localworkspace 提供本机文件的读写与命令执行，相对路径与命令工作目录以会话默认文件夹为起点，模型看到的是本机绝对路径。
package localworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

const (
	// maxReadBytes 是按文本读取单个文件的字节上限。
	maxReadBytes = 10 << 20
	// maxImageBytes 是以图片读取单个文件的字节上限。
	maxImageBytes = 10 << 20
	// binarySniffBytes 是判断二进制文件时检查的文件开头字节数。
	binarySniffBytes = 8000
	// maxSymlinkDepth 是写入时逐级解析符号链接的层数上限。
	maxSymlinkDepth = 40
)

// imageTypes 是可以按图片读取的内容类型。
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// Backend 读写本机文件并执行命令：绝对路径直接访问，~ 开头按用户主目录展开，相对路径以默认文件夹为起点。
type Backend struct {
	root string
	// environment 叠加在命令的基础环境变量上。
	environment Environment
	// writes 串行化写入、修改与删除，同一次运行并行的改动不互相覆盖。
	writes sync.Mutex
}

// New 以默认文件夹为相对路径起点打开本机文件访问，命令执行时在基础环境变量上叠加 environment。
func New(dir string, environment Environment) *Backend {
	return &Backend{root: filepath.Clean(dir), environment: environment}
}

// resolve 把模型给出的路径解析为本机绝对路径。
func (b *Backend) resolve(name string) (string, error) {
	switch {
	case name == "~" || strings.HasPrefix(name, "~/") || strings.HasPrefix(name, `~\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("无法确定用户主目录")
		}
		name = filepath.Join(home, name[1:])
	case !filepath.IsAbs(name):
		name = filepath.Join(b.root, name)
	}
	return filepath.Clean(name), nil
}

// LsInfo 列出目录的直接子项，目录路径以路径分隔符结尾；路径指向文件时返回该文件。
func (b *Backend) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	dir, err := b.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("无法访问：%s", dir)
	}
	if !info.IsDir() {
		return []filesystem.FileInfo{fileInfo(dir, info)}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("无法读取目录：%s", dir)
	}
	infos := make([]filesystem.FileInfo, 0, len(entries))
	for _, entry := range entries {
		// 符号链接按目标展示，目标不存在时只展示条目本身。
		name := filepath.Join(dir, entry.Name())
		info, err := os.Stat(name)
		if err != nil {
			if info, err = entry.Info(); err != nil {
				continue
			}
		}
		infos = append(infos, fileInfo(name, info))
	}
	return infos, nil
}

// fileInfo 转换文件信息，目录路径以路径分隔符结尾。
func fileInfo(name string, info os.FileInfo) filesystem.FileInfo {
	result := filesystem.FileInfo{Path: name, IsDir: info.IsDir(), Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339)}
	if info.IsDir() && !strings.HasSuffix(name, string(filepath.Separator)) {
		result.Path += string(filepath.Separator)
	}
	return result
}

// Read 按行读取文本文件，Offset 从 1 开始，Limit 为 0 时读到文件末尾；二进制文件与超出上限的文件返回错误。
func (b *Backend) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	file, err := b.resolve(req.FilePath)
	if err != nil {
		return nil, err
	}
	content, err := b.readFile(file, maxReadBytes)
	if err != nil {
		return nil, err
	}
	if isBinary(content) {
		return nil, fmt.Errorf("二进制文件无法按文本读取：%s", file)
	}
	lines := strings.SplitAfter(string(content), "\n")
	start := max(req.Offset, 1) - 1
	if start >= len(lines) {
		return &filesystem.FileContent{}, nil
	}
	end := len(lines)
	if req.Limit > 0 {
		end = min(end, start+req.Limit)
	}
	return &filesystem.FileContent{Content: strings.TrimSuffix(strings.Join(lines[start:end], ""), "\n")}, nil
}

// MultiModalRead 把图片文件读取为图片内容，其他文件按文本读取。
func (b *Backend) MultiModalRead(ctx context.Context, req *filesystem.MultiModalReadRequest) (*filesystem.MultiFileContent, error) {
	file, err := b.resolve(req.FilePath)
	if err != nil {
		return nil, err
	}
	mimeType, err := b.imageType(file)
	if err != nil {
		return nil, err
	}
	if mimeType == "" {
		content, err := b.Read(ctx, &req.ReadRequest)
		if err != nil {
			return nil, err
		}
		return &filesystem.MultiFileContent{FileContent: content}, nil
	}
	data, err := b.readFile(file, maxImageBytes)
	if err != nil {
		return nil, err
	}
	return &filesystem.MultiFileContent{Parts: []filesystem.FileContentPart{{Type: filesystem.FileContentPartTypeImage, MIMEType: mimeType, Data: data}}}, nil
}

// imageType 按普通文件开头内容识别图片类型，不是图片或不是普通文件时返回空串。
func (b *Backend) imageType(file string) (string, error) {
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return "", nil
	}
	handle, err := os.Open(file)
	if err != nil {
		return "", fileError("读取文件", file, err)
	}
	defer handle.Close()
	head := make([]byte, 512)
	n, _ := handle.Read(head)
	mimeType := http.DetectContentType(head[:n])
	if slices.Contains(imageTypes, mimeType) {
		return mimeType, nil
	}
	return "", nil
}

// readFile 读取普通文件，目录、管道与设备等非普通文件或超出字节上限时返回错误。
func (b *Backend) readFile(file string, limit int64) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, fileError("读取文件", file, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("路径是目录，请使用 ls 查看：%s", file)
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

// Write 以完整内容创建或覆盖文件，缺少的上级目录一并创建；指向符号链接时写入链接目标。
func (b *Backend) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	file, err := b.resolve(req.FilePath)
	if err != nil {
		return err
	}
	b.writes.Lock()
	defer b.writes.Unlock()
	// 排队期间运行已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fileError("创建目录", filepath.Dir(file), err)
	}
	return replaceFile(file, []byte(req.Content))
}

// Edit 把文本文件中的原文替换为新内容；原文必须存在，未要求全部替换时必须唯一。
func (b *Backend) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	file, err := b.resolve(req.FilePath)
	if err != nil {
		return err
	}
	if req.OldString == "" {
		return errors.New("要替换的原文不能为空")
	}
	if req.OldString == req.NewString {
		return errors.New("替换内容与原文相同")
	}
	b.writes.Lock()
	defer b.writes.Unlock()
	// 排队期间运行已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return err
	}
	content, err := b.readFile(file, maxReadBytes)
	if err != nil {
		return err
	}
	if isBinary(content) {
		return fmt.Errorf("二进制文件无法按文本修改：%s", file)
	}
	text := string(content)
	count := strings.Count(text, req.OldString)
	switch {
	case count == 0:
		return fmt.Errorf("文件中找不到要替换的原文：%s", file)
	case count > 1 && !req.ReplaceAll:
		return fmt.Errorf("原文在文件中出现 %d 次，请提供更多上下文使其唯一，或设置 replace_all：%s", count, file)
	}
	if req.ReplaceAll {
		text = strings.ReplaceAll(text, req.OldString, req.NewString)
	} else {
		text = strings.Replace(text, req.OldString, req.NewString, 1)
	}
	return replaceFile(file, []byte(text))
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

// Delete 删除一个文件或空文件夹；路径是符号链接时只删除链接本身。
func (b *Backend) Delete(ctx context.Context, name string) error {
	file, err := b.resolve(name)
	if err != nil {
		return err
	}
	b.writes.Lock()
	defer b.writes.Unlock()
	// 排队期间运行已取消时直接返回，文件保持原样。
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(file)
	if err != nil {
		return fmt.Errorf("文件不存在：%s", file)
	}
	if err := os.Remove(file); err != nil {
		if info.IsDir() {
			return fmt.Errorf("只能删除空文件夹：%s", file)
		}
		return fileError("删除", file, err)
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
