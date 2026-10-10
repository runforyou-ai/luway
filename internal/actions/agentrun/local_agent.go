//go:build server

package agentrun

import (
	"cmp"
	"context"
	"errors"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/uptrace/bun"
)

// Submit 在电脑在线时把当前工具调用交给会话中该本机 Agent 的会话并通知执行器，随后立即返回交给模型的结果；电脑离线时返回失败原因。
func (c *runComputer) Submit(ctx context.Context, operation domain.ComputerOperation) (string, error) {
	call, ok := einorun.CallFrom(ctx)
	if !ok {
		return "", errors.New("local agent turn requires a recorded tool call")
	}
	callID := call.RecordID
	if failure, err := c.checkDecided(ctx, callID); err != nil || failure != "" {
		return "", cmp.Or(err, errors.New(failure))
	}
	online := false
	err := realtime.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
		var err error
		online, err = localagent.Dispatch(ctx, tx, localagent.Turn{
			WorkspaceID: c.workspaceID, ConversationID: c.folder, AgentID: c.agentID, ComputerID: c.computerID,
			CallID: callID, Operation: c.Target(operation).Operation, From: domain.AgentToolCallRunning,
		})
		return err
	})
	if err != nil {
		return "", err
	}
	if !online {
		return "", agentcontract.ErrComputerOffline
	}
	return agentcontract.LocalAgentSubmitted(operation.LocalAgent), nil
}
