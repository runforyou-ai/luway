// buildcheck 执行跨平台的包依赖检查和日志静态检查。
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/runforyou-ai/support/arr"
)

const modulePrefix = "github.com/runforyou-ai/luway/"

// goRunner 执行指定环境中的 Go 命令并返回输出。
type goRunner func(env []string, args ...string) ([]byte, error)

// main 按任务类型执行检查并传播失败状态。
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法：buildcheck pkg|deps|slog:server|slog:desktop")
		os.Exit(2)
	}
	if err := check(os.Args[1], runGo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(os.Args[1] + " 检查通过")
}

// runGo 返回 Go 命令的标准输出，失败时错误只附带标准错误中的诊断。
func runGo(env []string, args ...string) ([]byte, error) {
	command := exec.Command("go", args...)
	command.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s：%w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return output, nil
}

// check 选择依赖检查或日志检查。
func check(mode string, run goRunner) error {
	switch mode {
	case "pkg":
		for _, tags := range []string{"", "testhash", "server", "server,testhash"} {
			output, err := run(nil, "list", "-deps", "-test", "-tags="+tags, "./pkg/...")
			if err != nil {
				return err
			}
			if err := validatePackages("pkg", output); err != nil {
				return fmt.Errorf("tags=%q：%w", tags, err)
			}
		}
		return nil
	case "deps":
		return checkTargets(run)
	case "slog:server", "slog:desktop":
		return checkLogs(mode, run)
	default:
		return fmt.Errorf("未知检查类型：%s", mode)
	}
}

// checkTargets 按各原生平台、执行器和服务端目标读取传递依赖，服务端只读取依赖关系，不要求嵌入的前端产物已构建。
func checkTargets(run goRunner) error {
	for _, target := range []string{"darwin", "windows", "linux", "ios", "android"} {
		tags, mode, cgo := "", "desktop", "1"
		if target == "ios" || target == "android" {
			tags, mode = target, "mobile"
		}
		if target == "windows" {
			cgo = "0"
		}
		output, err := run([]string{"GOOS=" + target, "GOARCH=arm64", "CGO_ENABLED=" + cgo}, "list", "-deps", "-tags="+tags, ".")
		if err != nil {
			return err
		}
		if err := validatePackages(mode, output); err != nil {
			return fmt.Errorf("GOOS=%s：%w", target, err)
		}
	}
	for _, target := range []string{"darwin", "windows", "linux"} {
		output, err := run([]string{"GOOS=" + target, "GOARCH=amd64", "CGO_ENABLED=0"}, "list", "-deps", "./cmd/executor")
		if err != nil {
			return err
		}
		if err := validatePackages("executor", output); err != nil {
			return fmt.Errorf("GOOS=%s：%w", target, err)
		}
		output, err = run([]string{"GOOS=" + target, "GOARCH=amd64", "CGO_ENABLED=0"}, "list", "-e", "-deps", "-tags=server", "./cmd/server")
		if err != nil {
			return err
		}
		if err := validatePackages("server", output); err != nil {
			return fmt.Errorf("GOOS=%s：%w", target, err)
		}
	}
	return nil
}

// validatePackages 校验依赖边界，realtime/protocol 是允许跨端引用的契约。
func validatePackages(mode string, output []byte) error {
	server := []string{"appservice/direct", "actions", "api", "httpcodec", "storage/server", "task", "ingress", "config/server", "publicweb", "realtime", "servertest", "integrationtest", "agentruntime", "servercli", "serverapp"}
	mobile := []string{"computerhost", "executor", "executorcli", "integration/mcp"}
	for _, pkg := range strings.Fields(string(output)) {
		if (mode == "executor" || mode == "server") && strings.HasPrefix(pkg, "github.com/wailsapp/wails/") {
			return fmt.Errorf("%s 不得依赖 %s", mode, pkg)
		}
		name, internal := strings.CutPrefix(pkg, modulePrefix+"internal/")
		if !internal {
			continue
		}
		if mode == "pkg" {
			return fmt.Errorf("pkg 不得依赖 %s", pkg)
		}
		if name == "realtime/protocol" || strings.HasPrefix(name, "realtime/protocol/") {
			continue
		}
		if mode == "server" {
			for _, prefix := range []string{"native", "computerhost", "storage/desktop"} {
				if name == prefix || strings.HasPrefix(name, prefix+"/") {
					return fmt.Errorf("%s 不得依赖 %s", mode, pkg)
				}
			}
			continue
		}
		denied := slices.Clone(server)
		if mode == "mobile" {
			denied = append(denied, mobile...)
		}
		if mode == "executor" {
			denied = append(denied, "native", "computerhost", "storage")
		}
		for _, prefix := range denied {
			if name == prefix || strings.HasPrefix(name, prefix+"/") {
				return fmt.Errorf("%s 不得依赖 %s", mode, pkg)
			}
		}
	}
	return nil
}

// checkLogs 编译宿主平台分析器并检查目标平台代码，结束时清理临时目录。
func checkLogs(mode string, run goRunner) error {
	dir, err := os.MkdirTemp("", "app-slogguard-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tool := filepath.Join(dir, "slogguard")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	if _, err := run([]string{"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH, "CGO_ENABLED=0"}, "build", "-o", tool, "./internal/tools/slogguard"); err != nil {
		return err
	}
	targets := []string{runtime.GOOS, "windows"}
	if mode == "slog:server" {
		targets = append(targets, "linux")
	}
	for _, target := range arr.Unique(targets) {
		cgo := "0"
		args := []string{"vet", "-vettool=" + tool}
		if mode == "slog:server" {
			args = append(args, "-tags=server")
		} else if target != "windows" {
			cgo = "1"
		}
		args = append(args, "./...")
		if _, err := run([]string{"GOOS=" + target, "GOARCH=" + runtime.GOARCH, "CGO_ENABLED=" + cgo}, args...); err != nil {
			return err
		}
	}
	return nil
}
