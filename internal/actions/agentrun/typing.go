//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// runTypingRefreshInterval 是运行期间刷新输入状态的间隔，短于各端的到期清除时间。
const runTypingRefreshInterval = 3 * time.Second

// runTypingAudience 缓存一次运行中不变的输入状态发送者与受众，在场成员每次发布时重新读取。
type runTypingAudience struct {
	run      *servermodels.AgentRun
	resolved bool
	// senderID 是 AI 员工的聊天主体编号。
	senderID string
	// channelSourced 表示服务周期来自外部渠道，不通知会话中的真人成员。
	channelSourced bool
	// visitorIdentityID 是网站渠道访客的渠道身份编号，其他来源为空。
	visitorIdentityID *string
}

// resolve 读取发送者，并确定服务周期是否来自外部渠道以及网站访客受众。
func (t *runTypingAudience) resolve(ctx context.Context, db bun.IDB) error {
	run := t.run
	if err := db.NewSelect().TableExpr("chat_subjects AS cs").
		Column("cs.id").
		Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", run.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, run.AgentIdentityID).
		Scan(ctx, &t.senderID); err != nil {
		return fmt.Errorf("load agent typing sender: %w", err)
	}
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		var channel struct {
			IdentityID *string `bun:"identity_id"`
		}
		err := db.NewSelect().TableExpr("channel_conversations AS cc").
			ColumnExpr("CASE WHEN c.type = ? THEN ci.id END AS identity_id", domain.ChannelTypeWebsite).
			Join("JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
			Join("JOIN channels AS c ON c.workspace_id = ci.workspace_id AND c.id = ci.channel_id").
			Where("cc.workspace_id = ? AND cc.conversation_id = ?", run.WorkspaceID, run.ConversationID).
			Scan(ctx, &channel)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load visitor typing audience: %w", err)
		}
		t.channelSourced, t.visitorIdentityID = err == nil, channel.IdentityID
	}
	t.resolved = true
	return nil
}

// notifications 构造 AI 员工输入状态通知：服务周期发往企业客服共享受众，网站渠道同时发往访客；非渠道来源的服务周期与其余会话发往在场的在职真人成员。
func (t *runTypingAudience) notifications(ctx context.Context, db bun.IDB, active bool) ([]realtime.Notification, error) {
	if !t.resolved {
		if err := t.resolve(ctx, db); err != nil {
			return nil, err
		}
	}
	run := t.run
	notifications := make([]realtime.Notification, 0, 2)
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		notifications = append(notifications, realtime.ServiceInboxConversationTyping(run.WorkspaceID, run.ConversationID, t.senderID, active))
	}
	// 渠道来源只另外通知网站访客，其他来源继续通知会话中的真人成员。
	if t.channelSourced {
		if t.visitorIdentityID != nil {
			notifications = append(notifications, realtime.VisitorDirectoryTyping(run.WorkspaceID, *t.visitorIdentityID, run.ConversationID, active))
		}
		return notifications, nil
	}
	var userIDs []string
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN users AS u ON u.workspace_id = cs.workspace_id AND u.identity_id = cs.source_id").
		Column("u.id").
		Where("cp.workspace_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", run.WorkspaceID, run.ConversationID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Scan(ctx, &userIDs); err != nil {
		return nil, fmt.Errorf("load member typing audience: %w", err)
	}
	for _, userID := range userIDs {
		notifications = append(notifications, realtime.UserConversationTyping(run.WorkspaceID, userID, run.ConversationID, t.senderID, active))
	}
	return notifications, nil
}

// runTypingPublisher 返回按当前受众发布一次 AI 员工输入状态的函数，读取受众失败时只记录日志；返回的函数由单个发布协程调用。
func (a *ExecuteAction) runTypingPublisher(ctx context.Context, run *servermodels.AgentRun) func(active bool) {
	audience := &runTypingAudience{run: run}
	return func(active bool) {
		notifications, err := audience.notifications(ctx, a.db, active)
		if err != nil {
			slog.WarnContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "读取 AI 员工输入状态受众失败", "agent_run_id", run.ID, "error", err)
			return
		}
		realtime.Publish(notifications...)
	}
}

// runTyping 在运行期间按间隔发布正在输入，停止时发布一次停止输入。
type runTyping struct {
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// startRunTyping 立即开始按间隔发布，持续到停止或 ctx 结束。
func startRunTyping(ctx context.Context, publish func(active bool), interval time.Duration) *runTyping {
	typing := &runTyping{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(typing.done)
		defer publish(false)
		publish(true)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-typing.stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				publish(true)
			}
		}
	}()
	return typing
}

// close 停止发布并等待停止输入发出；可并发、重复调用。
func (t *runTyping) close() {
	t.stopOnce.Do(func() { close(t.stop) })
	<-t.done
}

// startServerRunTyping 在服务端执行运行期间发布 AI 员工正在输入，返回停止发布的收尾函数。
func (a *ExecuteAction) startServerRunTyping(ctx context.Context, run *servermodels.AgentRun) func() {
	return startRunTyping(ctx, a.runTypingPublisher(context.WithoutCancel(ctx), run), runTypingRefreshInterval).close
}
