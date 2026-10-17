// Package diagnostic 生成包含运行摘要和本机日志的诊断包。
package diagnostic

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Build 打包运行摘要和最近的日志，日志总量最多二十 MiB。
func Build(directory string, summary any) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(manifest).Encode(summary); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(directory, "logs", "server-*.jsonl"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	slices.Reverse(files)
	remaining := int64(20 << 20)
	// 从最新文件开始收集，正在增长的文件读取已确定的长度。
	for _, path := range files {
		if remaining == 0 {
			break
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		size := min(info.Size(), remaining)
		if _, err := file.Seek(info.Size()-size, io.SeekStart); err != nil {
			file.Close()
			return nil, err
		}
		entry, err := archive.Create("logs/" + filepath.Base(path))
		if err != nil {
			file.Close()
			return nil, err
		}
		_, err = io.CopyN(entry, file, size)
		file.Close()
		if err != nil {
			return nil, err
		}
		remaining -= size
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
