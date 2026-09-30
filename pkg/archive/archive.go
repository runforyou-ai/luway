// Package archive 把 tar.gz 与 zip 压缩包安全解压到指定目录，条目不得越出该目录。
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// EntryPath 返回压缩包条目在解压目录中的路径，条目越出解压目录时返回错误。
func EntryPath(root, name string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(name))
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes target directory", name)
	}
	return path, nil
}

// ExtractTarGz 解压 tar.gz 中的目录、普通文件与相对符号链接：条目不经已解压的符号链接写入，解压后每个符号链接解析到的位置都在解压目录内。
func ExtractTarGz(archivePath, root string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	decompressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer decompressed.Close()
	reader := tar.NewReader(decompressed)
	var links []string
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		path, err := writablePath(root, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0o755)
		case tar.TypeReg:
			err = writeFile(path, reader, header.FileInfo().Mode().Perm())
		case tar.TypeSymlink:
			// 符号链接只接受指向解压目录内的相对目标。
			if filepath.IsAbs(header.Linkname) {
				return fmt.Errorf("archive symlink %q is absolute", header.Name)
			}
			if _, err := EntryPath(root, filepath.ToSlash(filepath.Join(filepath.Dir(header.Name), header.Linkname))); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			err = os.Symlink(header.Linkname, path)
			links = append(links, path)
		}
		if err != nil {
			return err
		}
	}
	// 符号链接可以经其他符号链接串联，按实际解析结果校验。
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, link := range links {
		if !within(realRoot, link) {
			return fmt.Errorf("archive symlink %q resolves outside target directory", link)
		}
	}
	return nil
}

// within 判断 path 经符号链接解析后的实际位置是否在 realRoot 内，realRoot 须是已解析的实际路径；path 指向不存在的位置时视为在内。
func within(realRoot, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return resolved == realRoot || strings.HasPrefix(resolved, realRoot+string(filepath.Separator))
}

// writablePath 返回条目路径，路径越出解压目录或经过已解压的符号链接时返回错误。
func writablePath(root, name string) (string, error) {
	path, err := EntryPath(root, name)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return path, err
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("archive entry %q passes through a symlink", name)
		}
	}
	return path, nil
}

// ExtractZip 解压 zip 中的目录与普通文件。
func ExtractZip(archivePath, root string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		path, err := writablePath(root, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		content, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeFile(path, content, entry.Mode().Perm()|0o600)
		content.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFile 创建上级目录并写入文件内容。
func writeFile(path string, content io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, content)
	return errors.Join(copyErr, file.Close())
}
