//go:build !server && !ios && !android

// Package executorcli 是无界面执行器程序的命令行入口。
package executorcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/locallog"
	"github.com/runforyou-ai/luway/internal/executor"
	"github.com/runforyou-ai/luway/internal/executor/localskill"
	"github.com/runforyou-ai/luway/internal/executor/localworkspace"
)

// errExecutorCredentialInvalid 表示服务端不再接受工作区电脑的凭据。
var errExecutorCredentialInvalid = errors.New("电脑凭据已失效：电脑已删除或凭据已重置，请用新凭据启动执行器")

// Run 以工作区电脑凭据连接服务端，在这台电脑上执行派发的操作，直到收到退出信号或凭据失效。
// 参数未给出时读取同名环境变量：EXECUTOR_SERVER_URL、EXECUTOR_CREDENTIAL、EXECUTOR_DATA_DIR、EXECUTOR_CONCURRENCY。
func Run(args []string) error {
	flags := flag.NewFlagSet(brand.Build().Slug+"-executor", flag.ContinueOnError)
	serverURL := flags.String("server", os.Getenv("EXECUTOR_SERVER_URL"), "服务器地址")
	credential := flags.String("credential", os.Getenv("EXECUTOR_CREDENTIAL"), "工作区电脑凭据")
	dataDir := flags.String("data-dir", os.Getenv("EXECUTOR_DATA_DIR"), "数据目录：会话文件夹、本机 MCP 配置 mcp.json 与技能目录 skills，默认是用户主目录下的 ."+brand.Build().Slug+"-executor")
	// 并发上限默认取 EXECUTOR_CONCURRENCY，未设置或无效时为 0。
	defaultConcurrency, _ := strconv.Atoi(os.Getenv("EXECUTOR_CONCURRENCY"))
	concurrency := flags.Int("concurrency", defaultConcurrency, "同时执行的操作上限，默认 4")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if *serverURL == "" || *credential == "" {
		return errors.New("需要服务器地址与工作区电脑凭据：-server、-credential 或 EXECUTOR_SERVER_URL、EXECUTOR_CREDENTIAL")
	}
	if _, ok := executor.Platform(); !ok {
		return errors.New("当前平台不能作为电脑")
	}
	// 未指定数据目录时使用用户主目录下以执行器命名的目录。
	if *dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory, set -data-dir or EXECUTOR_DATA_DIR: %w", err)
		}
		*dataDir = filepath.Join(home, "."+brand.Build().Slug+"-executor")
	}
	// 日志同时写入数据目录 logs 下的 executor.log，文件随进程退出关闭，Run 返回后入口记录的运行错误同样写入；无法打开文件时只写到标准错误输出。
	if _, err := locallog.Setup(filepath.Join(*dataDir, "logs", "executor.log")); err != nil {
		slog.WarnContext(context.Background(), "无法打开执行器日志文件，日志只写到标准错误输出", "error", err)
	}
	folderRoot := filepath.Join(*dataDir, "folders")
	if err := os.MkdirAll(folderRoot, 0o755); err != nil {
		return fmt.Errorf("create folder root: %w", err)
	}
	// 技能取数据目录的 skills 与本机共用的技能目录，本机 MCP 配置在数据目录的 mcp.json，修改后重启执行器生效；本机 Agent 配置在数据目录的 agents.json。
	skillDirs := []localskill.Dir{{Path: filepath.Join(*dataDir, "skills"), Source: localskill.SourceManaged}}
	if dirs, err := localskill.DefaultDirs(); err == nil {
		skillDirs = append(skillDirs, dirs[1:]...)
	} else {
		slog.WarnContext(context.Background(), "读取本机技能目录失败，只使用数据目录的技能", "error", err)
	}
	var host *executor.Host
	refresh := func() {
		if host != nil {
			host.Refresh()
		}
	}
	// 无界面执行器不托管运行环境，命令使用系统已安装的工具，也不提供浏览器与桌面驱动。
	host = executor.NewHost(executor.NewHostOptions(executor.LocalConfig{
		FolderRoot: folderRoot, DataDir: *dataDir, SkillDirs: skillDirs, OnChange: refresh,
		Toolchain: func() (localworkspace.Environment, bool) { return localworkspace.Environment{}, false },
	}))
	defer host.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	revoked := make(chan struct{})
	link, err := executor.StartLink(executor.LinkOptions{
		ServerURL: *serverURL, Credential: *credential, Host: host, Concurrency: *concurrency,
		OnCredentialInvalid: func() { close(revoked) },
	})
	if err != nil {
		return err
	}
	defer link.Stop()
	slog.InfoContext(ctx, "执行器已启动", "server", *serverURL, "folder_root", folderRoot)
	select {
	case <-ctx.Done():
		slog.InfoContext(ctx, "执行器收到退出信号")
		return nil
	case <-revoked:
		return errExecutorCredentialInvalid
	}
}
