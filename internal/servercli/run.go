//go:build server

package servercli

import (
	"context"
	"fmt"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
)

// runCommand 在前台运行服务端，控制台按启动配置的日志级别输出。
func runCommand(arguments []string, serve Serve) error {
	if err := parseFlags(newFlags("run", ""), arguments, 0); err != nil {
		return err
	}
	config, err := serverconfig.Load()
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	consoleLevel.Set(config.Log.SlogLevel())
	return serve(context.Background(), config)
}
