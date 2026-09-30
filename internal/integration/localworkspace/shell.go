package localworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"

	"github.com/runforyou-ai/cervi/pkg/outputbuffer"
)

const (
	// CommandTimeout 是单次命令的默认执行预算。
	CommandTimeout = 10 * time.Minute
	// maxOutputBytes 是命令输出保留的字节上限，超出时保留开头与结尾各一半。
	maxOutputBytes = 256 << 10
	// outputDrainTimeout 是命令进程树终止后读完剩余输出的时限。
	outputDrainTimeout = 2 * time.Second
)

// Execute 以默认文件夹为工作目录，在独立进程树中执行一条命令并返回合并后的标准输出与标准错误。
// 命令进程退出、超出预算或 context 取消时立即终止整个进程树，命令启动的后台进程随之终止。
func (b *Backend) Execute(ctx context.Context, req *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	timeout := CommandTimeout
	if req.Timeout != nil && *req.Timeout > 0 {
		timeout = *req.Timeout
	}
	// 命令可能改动文件，与写入、修改和删除串行执行；命令预算从取得执行权后开始计时。
	b.writes.Lock()
	defer b.writes.Unlock()
	// 排队期间运行已取消时直接返回，跳过执行命令。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	environment, err := commandEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(commandCtx, req.Command)
	cmd.Dir, cmd.Env = b.root, b.environment.apply(environment)
	output := outputbuffer.New(maxOutputBytes/2, maxOutputBytes/2)
	waitErr := runProcessTree(cmd, output)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	response := &filesystem.ExecuteResponse{Output: output.String(), Truncated: output.Truncated()}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		response.TimedOut = true
		return response, nil
	}
	if _, exited := errors.AsType[*exec.ExitError](waitErr); waitErr != nil && !exited {
		return nil, fmt.Errorf("命令执行失败：%w", waitErr)
	}
	exitCode := cmd.ProcessState.ExitCode()
	response.ExitCode = &exitCode
	return response, nil
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
