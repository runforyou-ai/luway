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
		Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id = ?", run.OrganizationID, domain.ChatSubjectKindOrganizationIdentity, run.AgentIdentityID).
		Scan(ctx, &t.senderID); err != nil {
		return fmt.Errorf("load agent typing sender: %w", err)
	}
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		var channel struct {
			IdentityID *string `bun:"identity_id"`
		}
		err := db.NewSelect().TableExpr("channel_conversations AS cc").
			ColumnExpr("CASE WHEN c.type = ? THEN cci.id END AS identity_id", domain.ChannelTypeWebsite).
			Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
			Join("JOIN channels AS c ON c.organization_id = cci.organization_id AND c.id = cci.channel_id").
			Where("cc.organization_id = ? AND cc.conversation_id = ?", run.OrganizationID, run.ConversationID).
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
		notifications = append(notifications, realtime.ServiceInboxConversationTyping(run.OrganizationID, run.ConversationID, t.senderID, active))
	}
	// 渠道来源只另外通知网站访客，其他来源继续通知会话中的真人成员。
	if t.channelSourced {
		if t.visitorIdentityID != nil {
			notifications = append(notifications, realtime.VisitorDirectoryTyping(run.OrganizationID, *t.visitorIdentityID, run.ConversationID, active))
		}
		return notifications, nil
	}
	var userIDs []string
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN users AS u ON u.organization_id = cs.organization_id AND u.identity_id = cs.source_id").
		Column("u.id").
		Where("cp.organization_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", run.OrganizationID, run.ConversationID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Scan(ctx, &userIDs); err != nil {
		return nil, fmt.Errorf("load member typing audience: %w", err)
	}
	for _, userID := range userIDs {
		notifications = append(notifications, realtime.UserConversationTyping(run.OrganizationID, userID, run.ConversationID, t.senderID, active))
	}
	return notifications, nil
}

// runTypingPublisher 返回按当前受众发布一次 AI 员工输入状态的函数，读取受众失败时只记录日志；返回的函数由单个发布协程调用。
func (a *ExecuteAction) runTypingPublisher(ctx context.Context, run *servermodels.AgentRun) func(active bool) {
	audience := &runTypingAudience{run: run}
	return func(active bool) {
		notifications, err := audience.notifications(ctx, a.db, active)
		if err != nil {
			slog.Warn("读取 AI 员工输入状态受众失败", "organization_id", run.OrganizationID, "agent_run_id", run.ID, "error", err)
			return
		}
		realtime.Publish(notifications...)
	}
}

// runTyping 在运行期间按间隔发布正在输入，截止时间到达或停止时发布一次停止输入。
type runTyping struct {
	mu       sync.Mutex
	deadline time.Time
	ended    bool
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// startRunTyping 立即开始按间隔发布；deadline 为零值表示持续到停止。
func startRunTyping(ctx context.Context, publish func(active bool), deadline time.Time, interval time.Duration) *runTyping {
	typing := &runTyping{deadline: deadline, stop: make(chan struct{}), done: make(chan struct{})}
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
				typing.end()
				return
			case now := <-ticker.C:
				// 截止判断与续期在同一把锁内完成。
				typing.mu.Lock()
				if !typing.deadline.IsZero() && !now.Before(typing.deadline) {
					typing.ended = true
				}
				ended := typing.ended
				typing.mu.Unlock()
				if ended {
					return
				}
				publish(true)
			}
		}
	}()
	return typing
}

// extend 把截止时间推迟到指定时刻，发布已结束时返回 false。
func (t *runTyping) extend(deadline time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return false
	}
	t.deadline = deadline
	return true
}

// end 标记发布结束，此后 extend 返回 false。
func (t *runTyping) end() {
	t.mu.Lock()
	t.ended = true
	t.mu.Unlock()
}

// close 停止发布并等待停止输入发出；可并发、重复调用。
func (t *runTyping) close() {
	t.end()
	t.stopOnce.Do(func() { close(t.stop) })
	<-t.done
}

// startServerRunTyping 在服务端执行运行期间发布 AI 员工正在输入，返回停止发布的收尾函数。
func (a *ExecuteAction) startServerRunTyping(ctx context.Context, run *servermodels.AgentRun) func() {
	return startRunTyping(ctx, a.runTypingPublisher(context.WithoutCancel(ctx), run), time.Time{}, runTypingRefreshInterval).close
}

// holdDeviceRunTyping 在设备持有租约期间发布 AI 员工正在输入，截止到租约到期；已在发布时只推迟截止时间，运行已不在进行时不发布。
func (a *ExecuteAction) holdDeviceRunTyping(ctx context.Context, run *servermodels.AgentRun, leaseExpiresAt time.Time) {
	if a.lockDeviceRunTyping(run.ID, leaseExpiresAt) {
		return
	}
	a.typingMu.Unlock()
	if !a.deviceRunHoldingLease(ctx, run) {
		return
	}
	// 查询期间并发续租可能已开始发布，此时合并为推迟截止时间。
	if a.lockDeviceRunTyping(run.ID, leaseExpiresAt) {
		return
	}
	publishCtx := context.WithoutCancel(ctx)
	typing := startRunTyping(publishCtx, a.runTypingPublisher(publishCtx, run), leaseExpiresAt, runTypingRefreshInterval)
	a.deviceTyping[run.ID] = typing
	a.typingMu.Unlock()
	go func() {
		<-typing.done
		a.typingMu.Lock()
		if a.deviceTyping[run.ID] == typing {
			delete(a.deviceTyping, run.ID)
		}
		a.typingMu.Unlock()
	}()
	// 注册后复核运行状态：收尾在注册前提交时由这里结束发布，在注册后提交时由收尾释放。
	if !a.deviceRunHoldingLease(ctx, run) {
		a.typingMu.Lock()
		if a.deviceTyping[run.ID] == typing {
			delete(a.deviceTyping, run.ID)
		}
		a.typingMu.Unlock()
		typing.close()
	}
}

// lockDeviceRunTyping 推迟进行中发布的截止时间并返回 true；没有进行中的发布时持有 typingMu 返回 false，遇到已结束的发布器先在锁外等它发出停止输入。
func (a *ExecuteAction) lockDeviceRunTyping(runID string, deadline time.Time) bool {
	for {
		a.typingMu.Lock()
		current := a.deviceTyping[runID]
		if current == nil {
			return false
		}
		if current.extend(deadline) {
			a.typingMu.Unlock()
			return true
		}
		delete(a.deviceTyping, runID)
		a.typingMu.Unlock()
		current.close()
	}
}

// deviceRunHoldingLease 返回设备运行是否仍在进行并持有租约，读取失败时记录日志并视为不持有。
func (a *ExecuteAction) deviceRunHoldingLease(ctx context.Context, run *servermodels.AgentRun) bool {
	holding, err := a.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.id = ? AND agr.status = ? AND agr.lease_expires_at > clock_timestamp()", run.ID, domain.AgentRunStatusRunning).
		Exists(ctx)
	if err != nil {
		slog.Warn("读取设备运行状态失败", "organization_id", run.OrganizationID, "agent_run_id", run.ID, "error", err)
		return false
	}
	return holding
}

// releaseDeviceRun 结束设备运行的输入状态发布并关闭其企业 MCP 连接。
func (a *ExecuteAction) releaseDeviceRun(runID string) {
	a.deviceMCP.release(runID)
	a.typingMu.Lock()
	typing := a.deviceTyping[runID]
	a.typingMu.Unlock()
	if typing != nil {
		typing.close()
	}
}
