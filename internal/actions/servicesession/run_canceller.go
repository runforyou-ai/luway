//go:build server

package servicesession

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ServiceSessionRunCanceller 在客服事务内收敛原负责人的 Agent 执行。
type ServiceSessionRunCanceller interface {
	CancelForServiceSession(context.Context, bun.IDB, string, string, string, domain.AgentRunErrorCode) ([]string, error)
}
