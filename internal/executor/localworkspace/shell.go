package localworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/runforyou-ai/support/iox"
)

const (
	// maxOutputBytes 是命令输出保留的字节上限，超出时保留开头与结尾各一半。
	maxOutputBytes = 256 << 10
	// outputDrainTimeout 是命令进程树终止后读完剩余输出的时限。
	outputDrainTimeout = 2 * time.Second
)

// Run 以默认文件夹为工作目录，在独立进程树中执行一条命令，返回合并后的标准输出与标准错误，并注明非零退出码、超时与截断。
// 命令进程退出或 ctx 结束时立即终止整个进程树，命令启动的后台进程随之终止；ctx 到达时限按超时返回已有输出，ctx 取消时返回错误。
func (w *Workspace) Run(ctx context.Context, command string) (string, error) {
	environment, err := commandEnvironment(ctx)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(w.root, 0o755); err != nil {
		return "", fileError("创建目录", w.root, err)
	}
	cmd := shellCommand(ctx, command)
	cmd.Dir, cmd.Env = w.root, w.environment.apply(environment)
	output := iox.NewHeadTailBuffer(maxOutputBytes/2, maxOutputBytes/2, "\n…（中间输出过长已省略）…\n")
	waitErr := runProcessTree(cmd, output)
	if errors.Is(ctx.Err(), context.Canceled) {
		return "", ctx.Err()
	}
	parts := make([]string, 0, 3)
	if text := output.String(); text != "" {
		parts = append(parts, text)
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		parts = append(parts, "[命令超时，已终止]")
	case waitErr != nil:
		if _, exited := errors.AsType[*exec.ExitError](waitErr); !exited {
			return "", fmt.Errorf("命令执行失败：%w", waitErr)
		}
		parts = append(parts, fmt.Sprintf("[命令以退出码 %d 结束]", cmd.ProcessState.ExitCode()))
	}
	if output.Truncated() {
		parts = append(parts, "[输出过长，已截断中间部分]")
	}
	if len(parts) == 0 {
		return "[命令执行成功，没有输出]", nil
	}
	return strings.Join(parts, "\n"), nil
}

// runProcessTree 在独立进程树中运行命令，合并后的标准输出与标准错误写入 output；
// 主进程退出或命令 context 取消时立即终止整个进程树，返回命令的启动或等待错误。
func runProcessTree(cmd *exec.Cmd, output io.Writer) error {
	// 输出经进程直接持有的管道写入，主进程退出时 Wait 立即返回，不等待仍持有管道的后台进程。
	reader, writer, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("无法创建命令输出管道：%w", err)
	}
	defer reader.Close()
	cmd.Stdout, cmd.Stderr = writer, writer
	tree, err := newProcessTree(cmd)
	if err != nil {
		writer.Close()
		return err
	}
	defer tree.close()
	cmd.Cancel = tree.kill
	err = tree.start()
	writer.Close()
	if err != nil {
		return err
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(output, reader)
	}()
	waitErr := cmd.Wait()
	_ = tree.kill()
	// 进程树终止后管道写端全部关闭；脱离进程树的进程仍持有管道时按时限放弃剩余输出。
	select {
	case <-drained:
	case <-time.After(outputDrainTimeout):
		reader.Close()
		<-drained
	}
	return waitErr
}
