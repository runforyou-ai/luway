//go:build !server && !ios && !android

package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

// sharedStateName 是会话文件夹中记录共享文件区副本同步基准的文件名，位于共享文件区副本之外。
const sharedStateName = ".shared-sync.json"

// sharedState 是共享文件区副本上次同步后的状态，按共享文件区内路径索引。
type sharedState struct {
	Files map[string]sharedEntry `json:"files"`
}

// sharedEntry 是一个文件上次同步后的内容摘要、字节数与修改时间，字节数与修改时间不变时视为内容未变。
type sharedEntry struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// syncReport 是一次同步的结果：写回共享文件区的文件与未能同步的文件。
type syncReport struct {
	writes   []domain.SharedFileWrite
	failures []string
}

// merge 合并另一次同步的结果。
func (r *syncReport) merge(other syncReport) {
	r.writes = append(r.writes, other.writes...)
	r.failures = append(r.failures, other.failures...)
}

// note 返回附在命令结果后交给模型的同步说明，没有写回与失败时为空。
func (r syncReport) note() string {
	lines := make([]string, 0, len(r.writes)+len(r.failures))
	for _, write := range r.writes {
		if write.SavedAs == write.Path {
			lines = append(lines, "已写回 shared/"+write.Path)
		} else {
			lines = append(lines, fmt.Sprintf("shared/%s 在共享文件区已被改动，电脑上的版本另存为 shared/%s", write.Path, write.SavedAs))
		}
	}
	lines = append(lines, arr.Map(r.failures, func(failure string) string { return "未能同步：" + failure })...)
	if len(lines) == 0 {
		return ""
	}
	return "\n\n[共享文件]\n" + strings.Join(lines, "\n")
}

// syncShared 在会话文件夹的共享文件区副本与服务端共享文件区之间做一次双向同步，同一会话文件夹的同步依次进行：
// 电脑上相对上次同步改动或新建的文件按上次同步的摘要写回，服务端在此期间已改动或删除时另存为冲突副本；
// 随后与服务端最新清单比对，电脑上未改动或已删除的文件取服务端内容，服务端已删除而电脑上未改动的文件在电脑上删除；
// 电脑上已删除的文件不删除服务端文件。下载或写回期间电脑上又被改动的文件保留电脑上的内容，留待下次同步。
func (l *Link) syncShared(ctx context.Context, operation appservice.ComputerOperationItem) (syncReport, error) {
	folder := l.options.Host.folder(operation.Operation.Folder)
	unlock, err := l.options.Host.sharedLocks.acquire(ctx, folder)
	if err != nil {
		return syncReport{}, err
	}
	defer unlock()
	dir := filepath.Join(folder, domain.SharedFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return syncReport{}, fmt.Errorf("create shared folder: %w", err)
	}
	state := loadSharedState(folder)
	local, err := scanShared(dir, state)
	if err != nil {
		return syncReport{}, err
	}
	remote, err := l.sharedManifest(ctx, operation.ID)
	if err != nil {
		return syncReport{}, err
	}
	var report syncReport
	// 写回电脑上的改动，另存为冲突副本的文件随后取服务端内容，服务端已删除时在电脑上删除。
	failed, conflicted := map[string]bool{}, map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(local)) {
		file := local[name]
		base, known := state.Files[name]
		if known && base.Hash == file.Hash || remote[name].Hash == file.Hash {
			continue
		}
		savedAs, err := l.writeShared(ctx, operation.ID, dir, name, file, base.Hash)
		if err != nil {
			if l.ctx.Err() != nil || errors.Is(err, errSyncUnavailable) {
				return report, err
			}
			report.failures = append(report.failures, fmt.Sprintf("shared/%s（%v）", name, err))
			failed[name] = true
			continue
		}
		report.writes = append(report.writes, domain.SharedFileWrite{Path: name, SavedAs: savedAs})
		conflicted[name] = savedAs != name
	}
	if len(report.writes) > 0 {
		if remote, err = l.sharedManifest(ctx, operation.ID); err != nil {
			return report, err
		}
	}
	next := sharedState{Files: map[string]sharedEntry{}}
	for _, name := range slices.Sorted(maps.Keys(remote)) {
		file := remote[name]
		current, present := local[name]
		switch {
		case present && current.Hash == file.Hash:
			next.Files[name] = sharedEntry{Hash: file.Hash, Size: current.Size, ModTime: current.ModTime}
			continue
		case failed[name]:
			// 未能写回的改动保留在电脑上，基准不变。
			if base, known := state.Files[name]; known {
				next.Files[name] = base
			}
			continue
		case present && changedSince(filepath.Join(dir, filepath.FromSlash(name)), current):
			if base, known := state.Files[name]; known {
				next.Files[name] = base
			}
			continue
		}
		var expected *sharedEntry
		if present {
			expected = &current
		}
		entry, err := l.downloadShared(ctx, dir, name, file, expected)
		if errors.Is(err, errLocalChanged) {
			if base, known := state.Files[name]; known {
				next.Files[name] = base
			}
			continue
		}
		if err != nil {
			if l.ctx.Err() != nil {
				return report, err
			}
			// 下载失败的文件保留原有基准，下次同步重试。
			if base, known := state.Files[name]; known {
				next.Files[name] = base
			}
			report.failures = append(report.failures, fmt.Sprintf("shared/%s（%v）", name, err))
			continue
		}
		next.Files[name] = entry
	}
	for name, file := range local {
		if _, exists := remote[name]; exists {
			continue
		}
		// 服务端已删除且电脑上未改动的文件，以及已另存为冲突副本的文件在电脑上删除；其余留在电脑上的文件保留原有基准，下次同步按它判断。
		base, known := state.Files[name]
		full := filepath.Join(dir, filepath.FromSlash(name))
		if !failed[name] && (conflicted[name] || known && base.Hash == file.Hash) && !changedSince(full, file) {
			err := os.Remove(full)
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				continue
			}
			// 删除失败时下次同步重试：已另存为冲突副本的文件以当前内容为基准，不再重复写回，其余保留原有基准。
			report.failures = append(report.failures, fmt.Sprintf("shared/%s（%v）", name, err))
			if conflicted[name] {
				next.Files[name] = file
				continue
			}
		}
		if known {
			next.Files[name] = base
		}
	}
	return report, saveSharedState(folder, next)
}

// errSyncUnavailable 表示服务端已不接受这次操作的同步：操作已结束或不在这台电脑上执行。
var errSyncUnavailable = errors.New("shared file sync unavailable")

// sharedManifest 读取操作所属会话共享文件区的最新清单，按路径索引。
func (l *Link) sharedManifest(ctx context.Context, operationID string) (map[string]appservice.ComputerSharedFile, error) {
	list, err := l.client.listComputerSharedFiles(ctx, operationID)
	if err != nil {
		return nil, syncError(err)
	}
	return arr.KeyBy(list.Files, func(file appservice.ComputerSharedFile) string { return file.Path }), nil
}

// writeShared 上传电脑上的一个文件并按上次同步的摘要写回，返回实际写入的共享文件区路径。
func (l *Link) writeShared(ctx context.Context, operationID, dir, name string, file sharedEntry, baseHash string) (string, error) {
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	upload, err := l.client.createComputerSharedUpload(ctx, operationID, appservice.ComputerSharedUploadInput{Path: name, ContentType: contentType, ByteSize: file.Size})
	if err != nil {
		return "", syncError(err)
	}
	content, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	defer content.Close()
	// 上传的内容与扫描时不一致或上传期间文件又被改动时放弃本次写回，留待下次同步。
	hash := sha256.New()
	if err := l.client.upload(ctx, upload.Request, io.TeeReader(io.LimitReader(content, file.Size), hash), file.Size); err != nil {
		return "", err
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.Hash || changedSince(filepath.Join(dir, filepath.FromSlash(name)), file) {
		return "", errors.New("文件在写回期间被改动")
	}
	committed, err := l.client.commitComputerSharedFile(ctx, operationID, appservice.ComputerSharedCommitInput{Path: name, FileID: upload.FileID, BaseHash: baseHash})
	if err != nil {
		return "", syncError(err)
	}
	return committed.Path, nil
}

// errLocalChanged 表示下载期间电脑上的文件又被改动，保留电脑上的内容。
var errLocalChanged = errors.New("shared file changed on the computer during download")

// downloadShared 把服务端文件下载到共享文件区副本：先写入临时文件并核对摘要，替换前确认电脑上的文件仍是扫描时的状态（expected 为空表示扫描时不存在），
// 已被改动时返回 errLocalChanged；返回同步后的状态。
func (l *Link) downloadShared(ctx context.Context, dir, name string, file appservice.ComputerSharedFile, expected *sharedEntry) (sharedEntry, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return sharedEntry{}, err
	}
	body, err := l.client.download(ctx, file.URL)
	if err != nil {
		return sharedEntry{}, err
	}
	defer body.Close()
	temporary, err := os.CreateTemp(filepath.Dir(target), ".download-*")
	if err != nil {
		return sharedEntry{}, err
	}
	defer os.Remove(temporary.Name())
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(temporary, hash), body)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return sharedEntry{}, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.Hash {
		return sharedEntry{}, errors.New("下载的内容与共享文件区不一致")
	}
	if expected != nil && changedSince(target, *expected) {
		return sharedEntry{}, errLocalChanged
	}
	if _, err := os.Lstat(target); expected == nil && err == nil {
		return sharedEntry{}, errLocalChanged
	}
	// 临时文件只对当前用户可读写，替换前改为普通文件的权限。
	if err := os.Chmod(temporary.Name(), 0o644); err != nil {
		return sharedEntry{}, err
	}
	if err := os.Rename(temporary.Name(), target); err != nil {
		return sharedEntry{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return sharedEntry{}, err
	}
	return sharedEntry{Hash: file.Hash, Size: info.Size(), ModTime: info.ModTime().UnixNano()}, nil
}

// syncError 把服务端不再接受同步的冲突响应转换为 errSyncUnavailable。
func syncError(err error) error {
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusConflict {
		return errSyncUnavailable
	}
	return err
}

// scanShared 列出共享文件区副本中的普通文件及其状态，字节数与修改时间与基准一致时沿用基准摘要，否则重新计算；跳过符号链接、临时文件与不合法的路径。
func scanShared(dir string, state sharedState) (map[string]sharedEntry, error) {
	files := map[string]sharedEntry{}
	err := filepath.WalkDir(dir, func(full string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".download-") {
			return err
		}
		relative, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		name, ok := domain.NormalizeConversationFilePath(filepath.ToSlash(relative))
		if !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		current := sharedEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if base, known := state.Files[name]; known && base.Size == current.Size && base.ModTime == current.ModTime {
			current.Hash = base.Hash
		} else if current.Hash, err = hashFile(full); err != nil {
			return err
		}
		files[name] = current
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan shared folder: %w", err)
	}
	return files, nil
}

// changedSince 判断文件在扫描之后又被改动或删除。
func changedSince(full string, scanned sharedEntry) bool {
	info, err := os.Stat(full)
	return err != nil || info.Size() != scanned.Size || info.ModTime().UnixNano() != scanned.ModTime
}

// hashFile 计算文件内容的 SHA-256 摘要。
func hashFile(full string) (string, error) {
	content, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer content.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, content); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// loadSharedState 读取会话文件夹的同步基准，不存在或无法解析时返回空基准。
func loadSharedState(folder string) sharedState {
	state := sharedState{Files: map[string]sharedEntry{}}
	data, err := os.ReadFile(filepath.Join(folder, sharedStateName))
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.WarnContext(context.Background(), "共享文件区同步基准无法读取，按空基准同步", "folder", folder, "error", err)
	}
	if err != nil || state.Files == nil {
		return sharedState{Files: map[string]sharedEntry{}}
	}
	return state
}

// saveSharedState 以先写临时文件再替换的方式保存会话文件夹的同步基准。
func saveSharedState(folder string, state sharedState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := filepath.Join(folder, sharedStateName+".tmp")
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return fmt.Errorf("save shared sync state: %w", err)
	}
	return os.Rename(temporary, filepath.Join(folder, sharedStateName))
}
