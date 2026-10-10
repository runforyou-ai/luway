// Package locallog 把桌面端与无界面执行器的日志同时写到标准错误输出与本机日志文件：文件按大小轮转，只保留一个旧文件。
package locallog

import (
	"io"
	"log/slog"
	"os"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/support/filex"
)

const (
	// maxFileSize 是日志文件轮转前的最大字节数。
	maxFileSize = 10 << 20
	// fileBackups 是保留的已轮转日志文件个数。
	fileBackups = 1
)

// Setup 把默认日志改为以文本格式同时写到标准错误输出与 path 指向的文件并附带日志作用域，返回关闭文件的函数；打开文件失败时保持原有日志输出并返回错误。
func Setup(path string) (func() error, error) {
	file, err := filex.OpenRotating(path, maxFileSize, fileBackups)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(slog.New(logscope.Handler(slog.NewTextHandler(io.MultiWriter(os.Stderr, file), nil))))
	return file.Close, nil
}
