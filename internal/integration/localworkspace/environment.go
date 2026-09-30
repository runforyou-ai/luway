package localworkspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// Environment 是命令执行时叠加在基础环境变量上的设置，零值表示不做改动。
type Environment struct {
	// PathPrefix 是依次前置到 PATH 的目录。
	PathPrefix []string
	// Variables 是覆盖同名变量的 KEY=VALUE 项。
	Variables []string
}

// apply 返回叠加后的环境变量，base 为 nil 时以当前进程的环境变量为基础。
func (e Environment) apply(base []string) []string {
	if len(e.PathPrefix) == 0 && len(e.Variables) == 0 {
		return base
	}
	if base == nil {
		base = os.Environ()
	}
	// Windows 的环境变量名不区分大小写。
	sameName := func(a, b string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	result := make([]string, 0, len(base)+len(e.Variables)+1)
	path := ""
	for _, entry := range base {
		name, value, _ := strings.Cut(entry, "=")
		if sameName(name, "PATH") {
			path = value
			continue
		}
		overridden := slices.ContainsFunc(e.Variables, func(variable string) bool {
			key, _, _ := strings.Cut(variable, "=")
			return sameName(key, name)
		})
		if !overridden {
			result = append(result, entry)
		}
	}
	result = append(result, e.Variables...)
	parts := slices.Clone(e.PathPrefix)
	if path != "" {
		parts = append(parts, path)
	}
	return append(result, "PATH="+strings.Join(parts, string(os.PathListSeparator)))
}

// Command 创建在叠加后的环境变量中运行的命令，可执行文件按叠加后的 PATH 查找，工作目录为 dir；ctx 结束时终止命令进程。
func Command(ctx context.Context, environment Environment, dir, name string, args ...string) (*exec.Cmd, error) {
	base, err := commandEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	env := environment.apply(base)
	if env == nil {
		env = os.Environ()
	}
	path, err := lookPath(name, env)
	if err != nil {
		return nil, err
	}
	cmd, err := executableCommand(ctx, path, args)
	if err != nil {
		return nil, err
	}
	cmd.Env, cmd.Dir = env, dir
	return cmd, nil
}

// Process 是在独立进程树中运行、经标准输入输出通信的命令。
type Process struct {
	// Stdin 写入命令的标准输入，关闭时终止整个进程树。
	Stdin io.WriteCloser
	// Stdout 读取命令的标准输出。
	Stdout io.ReadCloser

	cmd   *exec.Cmd
	tree  *processTree
	input io.WriteCloser
	once  sync.Once
}

// StartProcess 在叠加后的环境变量中启动命令，命令与其启动的全部子进程在同一进程树中，标准错误写入 stderr（为空时丢弃）；
// ctx 结束或关闭 Stdin 时终止整个进程树。
func StartProcess(ctx context.Context, environment Environment, dir string, stderr io.Writer, name string, args ...string) (*Process, error) {
	cmd, err := Command(ctx, environment, dir, name, args...)
	if err != nil {
		return nil, err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("无法创建命令输入管道：%w", err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("无法创建命令输出管道：%w", err)
	}
	cmd.Stderr = stderr
	tree, err := newProcessTree(cmd)
	if err != nil {
		return nil, err
	}
	cmd.Cancel = tree.kill
	if err := tree.start(); err != nil {
		tree.close()
		return nil, err
	}
	process := &Process{Stdout: output, cmd: cmd, tree: tree, input: input}
	process.Stdin = processInput{process}
	return process, nil
}

// Close 关闭标准输入，终止整个进程树并等待命令退出。
func (p *Process) Close() error {
	p.once.Do(func() {
		_ = p.input.Close()
		_ = p.tree.kill()
		_ = p.cmd.Wait()
		p.tree.close()
	})
	return nil
}

// processInput 是命令的标准输入，关闭时终止整个进程树。
type processInput struct{ process *Process }

// Write 写入命令的标准输入。
func (i processInput) Write(p []byte) (int, error) { return i.process.input.Write(p) }

// Close 终止整个进程树。
func (i processInput) Close() error { return i.process.Close() }

// lookPath 在环境变量的 PATH 中查找可执行文件，名称含路径分隔符时直接检查该路径；Windows 按 PATHEXT 补全扩展名。
func lookPath(name string, env []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return exec.LookPath(name)
	}
	path := ""
	for _, entry := range env {
		if key, value, _ := strings.Cut(entry, "="); strings.EqualFold(key, "PATH") && (runtime.GOOS == "windows" || key == "PATH") {
			path = value
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if found, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf("找不到命令 %s", name)
}
