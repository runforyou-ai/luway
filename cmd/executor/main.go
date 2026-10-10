//go:build !server && !ios && !android

// 无界面执行器程序入口。
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/executorcli"
)

// main 运行执行器并记录无法恢复的运行错误。
func main() {
	// 日志以文本格式写到标准错误输出并附带日志作用域。
	slog.SetDefault(slog.New(logscope.Handler(slog.NewTextHandler(os.Stderr, nil))))
	if err := executorcli.Run(os.Args[1:]); err != nil {
		slog.ErrorContext(context.Background(), "运行失败", "error", err)
		os.Exit(1)
	}
}
