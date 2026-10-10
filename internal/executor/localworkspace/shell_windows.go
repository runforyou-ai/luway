//go:build windows

package localworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// commandEnvironment 返回命令的环境变量，为空表示继承当前进程的环境变量。
func commandEnvironment(context.Context) ([]string, error) {
	return nil, nil
}

// shellCommand 用 PowerShell 执行命令并以 UTF-8 输出，优先使用 PowerShell 7。
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	shell := "powershell.exe"
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = path
	}
	return exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; "+command)
}

// executableCommand 创建运行可执行文件的命令，ctx 结束时终止命令进程；.cmd 与 .bat 经 cmd.exe 执行，参数无法安全转义时返回错误。
func executableCommand(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".cmd" && extension != ".bat" {
		return exec.CommandContext(ctx, path, args...), nil
	}
	interpreter := os.Getenv("ComSpec")
	if interpreter == "" {
		interpreter = "cmd.exe"
	}
	line, err := cmdScriptLine(interpreter, path, args)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, interpreter)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return cmd, nil
}

// processTree 是容纳命令进程及其全部子进程的 Job Object，关闭时终止其中的全部进程。
type processTree struct {
	cmd *exec.Cmd
	job windows.Handle
}

// newProcessTree 创建关闭时终止全部进程的 Job Object，命令以挂起状态启动。
func newProcessTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("无法创建进程组：%w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("无法设置进程组：%w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &processTree{cmd: cmd, job: job}, nil
}

// start 以挂起状态启动命令，加入 Job Object 后恢复运行，命令创建的子进程都在 Job Object 中。
func (t *processTree) start() error {
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("无法启动命令：%w", err)
	}
	if err := t.attach(); err != nil {
		_ = t.kill()
		_ = t.cmd.Wait()
		return err
	}
	return nil
}

// attach 把挂起的命令进程加入 Job Object 并恢复其全部线程。
func (t *processTree) attach() error {
	pid := uint32(t.cmd.Process.Pid)
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("无法登记命令进程：%w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(t.job, process); err != nil {
		return fmt.Errorf("无法登记命令进程：%w", err)
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("无法恢复命令进程：%w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("无法恢复命令进程：%w", err)
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if err != nil {
			return fmt.Errorf("无法恢复命令进程：%w", err)
		}
		resumed = true
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) || !resumed {
		return fmt.Errorf("无法恢复命令进程：%v", err)
	}
	return nil
}

// kill 终止 Job Object 中的全部进程，并直接终止尚未加入 Job Object 的命令进程。
func (t *processTree) kill() error {
	err := windows.TerminateJobObject(t.job, 1)
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return err
}

// close 关闭 Job Object，仍在运行的进程随之终止。
func (t *processTree) close() {
	windows.CloseHandle(t.job)
}
