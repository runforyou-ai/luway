//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrPersonalAgentNotFound 表示当前企业中不存在指定个人 AI 员工，或当前成员不是其负责人。
	ErrPersonalAgentNotFound = errors.New("personal agent not found")
	// ErrPersonalAgentComputerNotFound 表示当前成员名下不存在指定的未撤销电脑。
	ErrPersonalAgentComputerNotFound = errors.New("personal agent computer not found")
	// ErrPersonalAgentResponsibleInactive 表示负责人已停用，个人 AI 员工不能恢复。
	ErrPersonalAgentResponsibleInactive = errors.New("personal agent responsible inactive")
)

// personalAgentCondition 限定 agents 记录为个人 AI 员工，调用方查询的 agents 别名为 a。
var personalAgentCondition = "'" + string(domain.ServiceAudiencePersonal) + "' = ANY(a.service_audiences)"

// PersonalAgentInput 定义个人 AI 员工的资料、执行配置与企业 MCP 服务，AvatarFileID 为空时保留当前头像。
type PersonalAgentInput struct {
	DisplayName  string
	AvatarFileID string
	Execution    ExecutionInput
	MCPServerIDs []string
}

// PersonalAgent 定义个人 AI 员工信息。
type PersonalAgent struct {
	ID                     string                `bun:"id"`
	IdentityID             string                `bun:"identity_id"`
	DisplayName            string                `bun:"display_name"`
	AvatarFileID           *string               `bun:"avatar_file_id"`
	ResponsibleUserID      string                `bun:"responsible_user_id"`
	ResponsibleIdentityID  string                `bun:"responsible_identity_id"`
	ResponsibleDisplayName string                `bun:"responsible_display_name"`
	ComputerID             string                `bun:"computer_id"`
	ComputerName           string                `bun:"computer_name"`
	ComputerRevokedAt      *time.Time            `bun:"computer_revoked_at"`
	ComputerLastSeenAt     *time.Time            `bun:"computer_last_seen_at"`
	Status                 domain.IdentityStatus `bun:"status"`
	PausedAt               *time.Time            `bun:"paused_at"`
	CreatedAt              time.Time             `bun:"created_at"`
	Execution              ExecutionSummary      `bun:"-"`
}

// Presence 按账号状态、暂停、电脑的撤销状态与最近在线时间计算个人 AI 员工当前能否处理新请求与操作电脑。
func (a PersonalAgent) Presence(now time.Time) domain.PersonalAgentPresence {
	return domain.ResolvePersonalAgentPresence(a.Status, a.PausedAt != nil, a.ComputerRevokedAt != nil, a.ComputerLastSeenAt, now)
}

// personalAgentQuery 构造个人 AI 员工及其负责人、使用的电脑的读取查询。
func personalAgentQuery(db bun.IDB, organizationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS id, a.identity_id::text AS identity_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, a.status, a.paused_at, oi.created_at").
		ColumnExpr("a.responsible_user_id::text AS responsible_user_id, responsible.id::text AS responsible_identity_id, responsible.display_name AS responsible_display_name").
		ColumnExpr("a.computer_id::text AS computer_id, cmp.name AS computer_name, cmp.revoked_at AS computer_revoked_at, cmp.last_seen_at AS computer_last_seen_at").
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id").
		Join("JOIN users AS u ON u.id = a.responsible_user_id AND u.organization_id = a.organization_id").
		Join("JOIN organization_identities AS responsible ON responsible.id = u.identity_id AND responsible.organization_id = u.organization_id").
		Join("JOIN computers AS cmp ON cmp.id = a.computer_id AND cmp.organization_id = a.organization_id").
		Where("a.organization_id = ?", organizationID).
		Where(personalAgentCondition)
}

// loadPersonalAgent 读取当前企业中的个人 AI 员工详情，responsibleUserID 非空时只返回该成员负责的个人 AI 员工。
func loadPersonalAgent(ctx context.Context, db bun.IDB, organizationID, agentID, responsibleUserID string) (*PersonalAgent, error) {
	if !common.ValidUUID(agentID) {
		return nil, ErrPersonalAgentNotFound
	}
	query := personalAgentQuery(db, organizationID).Where("a.id = ?", agentID)
	if responsibleUserID != "" {
		query = query.Where("a.responsible_user_id = ?", responsibleUserID)
	}
	personalAgent := &PersonalAgent{}
	if err := query.Scan(ctx, personalAgent); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	} else if err != nil {
		return nil, err
	}
	summaries, err := loadAgentExecutionSummaries(ctx, db, organizationID, []string{personalAgent.ID})
	if err != nil {
		return nil, err
	}
	personalAgent.Execution = summaries[personalAgent.ID]
	return personalAgent, nil
}

// lockOwnPersonalAgent 锁定当前成员负责的个人 AI 员工记录。
func lockOwnPersonalAgent(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, agentID string) (*servermodels.Agent, error) {
	if !common.ValidUUID(agentID) {
		return nil, ErrPersonalAgentNotFound
	}
	stored := &servermodels.Agent{}
	err := tx.NewSelect().Model(stored).
		Where("a.organization_id = ? AND a.id = ? AND a.responsible_user_id = ?", identity.Organization.ID, agentID, identity.User.ID).
		Where(personalAgentCondition).
		For("UPDATE OF a").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock personal agent: %w", err)
	}
	return stored, nil
}

// lockOwnActiveComputer 对当前成员名下未撤销的电脑取共享锁。
func lockOwnActiveComputer(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, computerID string) error {
	if !common.ValidUUID(computerID) {
		return ErrPersonalAgentComputerNotFound
	}
	exists, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
		Where("cmp.organization_id = ? AND cmp.owner_user_id = ? AND cmp.id = ? AND cmp.revoked_at IS NULL", identity.Organization.ID, identity.User.ID, computerID).
		For("SHARE").
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("lock personal agent computer: %w", err)
	}
	if !exists {
		return ErrPersonalAgentComputerNotFound
	}
	return nil
}
