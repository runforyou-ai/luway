package localskill

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
)

// ErrArchiveTooLarge 表示技能压缩包展开后的字节数或条目数超过上限。
var ErrArchiveTooLarge = errors.New("技能压缩包展开后超过大小或条目数上限")

const (
	// maxDownloadBytes 是从技能地址或 GitHub 仓库下载的文件大小上限。
	maxDownloadBytes = 512 << 20
	// maxExpandedBytes 是技能压缩包展开后的总字节数上限。
	maxExpandedBytes = 2 << 30
	// maxArchiveEntries 是技能压缩包的条目数上限，覆盖整仓库下载的 GitHub 压缩包。
	maxArchiveEntries = 100000
)

// checkZip 在解压前按实际解压出的数据统计 zip 的条目数与展开字节数，超过上限时返回 ErrArchiveTooLarge。
func checkZip(file string) error {
	reader, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveEntries {
		return ErrArchiveTooLarge
	}
	var total int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		content, err := entry.Open()
		if err != nil {
			return err
		}
		n, err := io.Copy(io.Discard, io.LimitReader(content, maxExpandedBytes-total+1))
		content.Close()
		total += n
		if err != nil {
			return err
		}
		if total > maxExpandedBytes {
			return ErrArchiveTooLarge
		}
	}
	return nil
}

// countingReader 统计已读出的字节数。
type countingReader struct {
	reader io.Reader
	count  int64
}

// Read 读取数据并累计字节数。
func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += int64(n)
	return n, err
}

// checkTarGz 在解压前统计 tar.gz 的条目数、整个 gzip 解压流的字节数与各条目实际读出的字节数，任一超过上限时返回 ErrArchiveTooLarge。
func checkTarGz(file string) error {
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gz.Close()
	// 解压流字节数覆盖 tar 头、条目数据与结束标记之后的 gzip 数据；条目字节数另含稀疏文件由 tar 读取器补出的零字节。
	stream := &countingReader{reader: io.LimitReader(gz, maxExpandedBytes+1)}
	archive := tar.NewReader(stream)
	var expanded int64
	for entries := 0; ; {
		_, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if _, err := io.Copy(io.Discard, stream); err != nil {
				return err
			}
			break
		}
		if err == nil {
			entries++
			if entries > maxArchiveEntries {
				return ErrArchiveTooLarge
			}
			var n int64
			n, err = io.Copy(io.Discard, io.LimitReader(archive, maxExpandedBytes-expanded+1))
			expanded += n
		}
		if stream.count > maxExpandedBytes || expanded > maxExpandedBytes {
			return ErrArchiveTooLarge
		}
		if err != nil {
			return err
		}
	}
	if stream.count > maxExpandedBytes {
		return ErrArchiveTooLarge
	}
	return nil
}
