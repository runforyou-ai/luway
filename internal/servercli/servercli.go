//go:build server

// Package servercli 实现服务端程序的命令：前台运行、部署状态、启动配置管理、客户端文件下载、部署地址修改、数据库迁移与部署检查。
package servercli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// Serve 在前台运行服务端，ctx 取消或服务端自行退出时返回。
type Serve func(ctx context.Context, config serverconfig.Config) error

// Program 定义服务端程序的组成：程序的数据库迁移与前台运行服务端的入口。
type Program struct {
	Migrations serverstorage.Migrations
	Serve      Serve
}

// command 是一个子命令。
type command struct {
	name    string
	summary string
	run     func(arguments []string) error
}

// consoleLevel 是控制台日志的输出级别。
var consoleLevel = new(slog.LevelVar)

// errUsage 表示命令行用法错误，已输出用法说明。
var errUsage = errors.New("命令行用法错误")

// Run 执行 arguments 指定的子命令，run 子命令经 program.Serve 运行服务端，其余子命令按 program.Migrations 读取与校验数据库迁移。
func Run(arguments []string, program Program) error {
	// 日志以文本格式写到标准错误输出并附带日志作用域，管理命令只输出警告与错误，run 命令按启动配置的日志级别输出。
	consoleLevel.Set(slog.LevelWarn)
	if len(arguments) > 0 && arguments[0] == "run" {
		consoleLevel.Set(slog.LevelInfo)
	}
	slog.SetDefault(slog.New(logscope.Handler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: consoleLevel}))))
	commands := []command{
		{"run", "在前台运行服务端", func(arguments []string) error { return runCommand(arguments, program.Serve) }},
		{"status", "查看部署版本与部署中的服务器", func(arguments []string) error { return statusCommand(arguments, program.Migrations) }},
		{"config", "查看、校验与修改启动配置", configCommand},
		{"download-clients", "下载与本程序同版本的发布清单与客户端文件，在服务端停止时执行", downloadClientsCommand},
		{"preflight", "检查本程序能否加入部署", func(arguments []string) error { return preflightCommand(arguments, program.Migrations) }},
		{"migrate", "查看数据库迁移状态或撤销较低版本没有的迁移", func(arguments []string) error { return migrateCommand(arguments, program.Migrations) }},
		{"public-url", "修改部署地址，管理端无法访问时使用", func(arguments []string) error { return publicURLCommand(arguments, program.Migrations) }},
		{"reset-server-id", "为复制数据库搭建的新平台生成新的服务器标识", func(arguments []string) error { return resetServerIDCommand(arguments, program.Migrations) }},
		{"version", "输出版本号与数据库迁移版本", func(arguments []string) error { return versionCommand(arguments, program.Migrations) }},
	}
	if len(arguments) == 0 {
		printUsage(os.Stderr, commands)
		return errUsage
	}
	if arguments[0] == "help" || arguments[0] == "-h" || arguments[0] == "--help" {
		printUsage(os.Stdout, commands)
		return nil
	}
	for _, candidate := range commands {
		if candidate.name == arguments[0] {
			err := candidate.run(arguments[1:])
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
	}
	printUsage(os.Stderr, commands)
	return fmt.Errorf("未知命令 %q", arguments[0])
}

// printUsage 输出命令列表。
func printUsage(output io.Writer, commands []command) {
	program := brand.Build().Slug + "-server"
	fmt.Fprintf(output, "用法：%s <命令> [参数]\n\n命令：\n", program)
	for _, candidate := range commands {
		fmt.Fprintf(output, "  %-16s %s\n", candidate.name, candidate.summary)
	}
	fmt.Fprintf(output, "\n执行 %s <命令> -h 查看命令参数。\n", program)
}

// newFlags 创建子命令的参数解析器，usage 为参数之外的位置参数说明。
func newFlags(name, usage string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法：%s-server %s [参数] %s\n", brand.Build().Slug, name, usage)
		flags.PrintDefaults()
	}
	return flags
}

// parseFlags 解析子命令参数，wantArgs 为允许的位置参数个数，-1 表示不限。
func parseFlags(flags *flag.FlagSet, arguments []string, wantArgs int) error {
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if wantArgs >= 0 && flags.NArg() != wantArgs {
		flags.Usage()
		return errUsage
	}
	return nil
}
