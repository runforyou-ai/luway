//go:build server

package groupchat

import (
	"context"

	"github.com/uptrace/bun"
)

// GroupAgentRunCanceller 在群成员变化事务内收敛受影响 AI 员工的执行。
type GroupAgentRunCanceller interface {
	CancelForGroupAgent(context.Context, bun.IDB, string, string, string) error
	CancelForGroupConversation(context.Context, bun.IDB, string, string) error
}
