package locallog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/common/logscope"
)

// DailyWriter 按 UTC 日期保存当前进程的 JSON 日志，并保留七天。
type DailyWriter struct {
	mu        sync.Mutex
	directory string
	instance  string
	day       string
	file      *os.File
}

// SetupServer 把服务端日志写入控制台与数据目录，附带版本、主机与进程身份。
func SetupServer(directory, instance, hostname, version string, level slog.Level) (*DailyWriter, error) {
	directory = filepath.Join(directory, "logs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	writer := &DailyWriter{directory: directory, instance: instance}
	if _, err := writer.Write(nil); err != nil {
		return nil, err
	}
	handler := slog.NewJSONHandler(io.MultiWriter(os.Stdout, writer), &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		failure, ok := attr.Value.Any().(error)
		if attr.Key != "error" || !ok {
			return attr
		}
		// 异常结构保留类型、消息与异常产生时的调用栈。
		exception := map[string]any{"type": fmt.Sprintf("%T", failure), "message": failure.Error()}
		var traced interface{ StackTrace() []uintptr }
		if errors.As(failure, &traced) {
			frames := runtime.CallersFrames(traced.StackTrace())
			var stack strings.Builder
			for {
				frame, more := frames.Next()
				fmt.Fprintf(&stack, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
				if !more {
					break
				}
			}
			exception["stacktrace"] = stack.String()
		}
		return slog.Any("exception", exception)
	}})
	slog.SetDefault(slog.New(logscope.Handler(handler)).With("service", "luway", "host", hostname, "instance_id", instance, "version", version))
	return writer, nil
}

// Write 把日志写入当天文件，并在日期切换时清理过期文件。
func (w *DailyWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UTC()
	day := now.Format(time.DateOnly)
	if w.day != day {
		file, err := os.OpenFile(filepath.Join(w.directory, "server-"+day+"-"+w.instance+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return 0, err
		}
		previous := w.file
		w.file, w.day = file, day
		if previous != nil {
			if err := previous.Close(); err != nil {
				return 0, err
			}
		}
		entries, err := os.ReadDir(w.directory)
		if err != nil {
			return 0, err
		}
		cutoff := now.AddDate(0, 0, -6).Format(time.DateOnly)
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && strings.HasPrefix(name, "server-") && strings.HasSuffix(name, ".jsonl") && len(name) > 17 && name[7:17] < cutoff {
				if err := os.Remove(filepath.Join(w.directory, name)); err != nil && !os.IsNotExist(err) {
					return 0, err
				}
			}
		}
	}
	return w.file.Write(data)
}

// Close 关闭当前日志文件。
func (w *DailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
