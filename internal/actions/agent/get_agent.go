//go:build server

package agent

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetAgentQuery 读取当前企业的 AI 员工详情。
type GetAgentQuery struct{ db *bun.DB }

// NewGetAgentQuery 创建 AI 员工详情查询。
func NewGetAgentQuery(db *bun.DB) *GetAgentQuery { return &GetAgentQuery{db: db} }

// Execute 返回当前企业的指定 AI 员工。
func (q *GetAgentQuery) Execute(ctx context.Context, identity *servermodels.Identity, agentID string) (*Agent, error) {
	agent, err := loadAgent(ctx, q.db, identity.Organization.ID, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return agent, nil
}
