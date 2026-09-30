//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// AuthorizeAgentRunStreamQuery 校验成员对一次运行所属会话的阅读资格。
type AuthorizeAgentRunStreamQuery struct {
	db *bun.DB
}

// NewAuthorizeAgentRunStreamQuery 创建运行过程流授权查询。
func NewAuthorizeAgentRunStreamQuery(db *bun.DB) *AuthorizeAgentRunStreamQuery {
	return &AuthorizeAgentRunStreamQuery{db: db}
}

// Execute 返回运行所属会话编号；运行不存在、不属于本企业或会话不可读时返回 ErrAgentRunProcessUnavailable。
func (q *AuthorizeAgentRunStreamQuery) Execute(ctx context.Context, identity *servermodels.Identity, runID string) (string, error) {
	if !common.ValidUUID(runID) {
		return "", ErrAgentRunProcessUnavailable
	}
	var conversationID string
	err := q.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Column("agr.conversation_id").
		Where("agr.organization_id = ? AND agr.id = ?", identity.Organization.ID, runID).
		Scan(ctx, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAgentRunProcessUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("load agent run conversation: %w", err)
	}
	// 会话不可读与运行不存在对外是同一结果，不暴露运行是否存在。
	if err := AuthorizeConversationHistory(ctx, q.db, identity, conversationID); err != nil {
		if errors.Is(err, ErrConversationNotFound) {
			return "", ErrAgentRunProcessUnavailable
		}
		return "", fmt.Errorf("authorize agent run stream: %w", err)
	}
	return conversationID, nil
}
