//go:build server

// Package ratelimit 按 GCRA 算法在 PostgreSQL 中限制公开入口的请求频率，多台服务端共用同一份额度。
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

const (
	// PruneActionName 是删除额度已全部恢复的限速记录的后台任务。
	PruneActionName = "rate_limit.prune"
	// PruneScheduleKey 是删除限速记录的定时计划标识。
	PruneScheduleKey = "rate-limit-prune"
)

// Rule 定义一类请求的限速：每隔 interval 恢复一次额度，最多累积 burst 次。
type Rule struct {
	name     string
	interval time.Duration
	burst    int
}

var (
	// VisitorMessageByIdentity 限制同一渠道访客发送文字与附件消息。
	VisitorMessageByIdentity = Rule{name: "visitor_message_identity", interval: 2 * time.Second, burst: 20}
	// VisitorMessageByIP 限制同一 IP 在一个渠道发送文字与附件消息。
	VisitorMessageByIP = Rule{name: "visitor_message_ip", interval: 500 * time.Millisecond, burst: 60}
	// VisitorUploadByIdentity 限制同一渠道访客创建附件上传。
	VisitorUploadByIdentity = Rule{name: "visitor_upload_identity", interval: 10 * time.Second, burst: 10}
	// VisitorUploadByIP 限制同一 IP 在一个渠道创建附件上传。
	VisitorUploadByIP = Rule{name: "visitor_upload_ip", interval: 2 * time.Second, burst: 30}
	// HelpSearchByIP 限制同一 IP 在一个渠道检索帮助中心。
	HelpSearchByIP = Rule{name: "help_search_ip", interval: 3 * time.Second, burst: 10}
	// HelpSearchByChannel 限制一个渠道的帮助中心检索总量。
	HelpSearchByChannel = Rule{name: "help_search_channel", interval: 100 * time.Millisecond, burst: 100}
	// LoginByIP 限制同一 IP 的登录尝试。
	LoginByIP = Rule{name: "login_ip", interval: 6 * time.Second, burst: 20}
	// LoginByEmail 限制同一邮箱的登录尝试。
	LoginByEmail = Rule{name: "login_email", interval: time.Minute, burst: 10}
	// RegisterByIP 限制同一 IP 注册账号。
	RegisterByIP = Rule{name: "register_ip", interval: time.Minute, burst: 10}
)

// For 返回按 subject 各部分限速的检查项，任一部分为空时不检查。
func (r Rule) For(subject ...string) Check {
	for _, part := range subject {
		if part == "" {
			return Check{}
		}
	}
	return Check{rule: r, key: r.name + ":" + strings.Join(subject, ":")}
}

// Check 是对一个限速键消耗一次额度的检查项。
type Check struct {
	rule Rule
	key  string
}

// LimitedError 表示请求超出限速，RetryAfter 后可以重试。
type LimitedError struct {
	RetryAfter time.Duration
}

// Error 返回限速错误描述。
func (e *LimitedError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}

// Limiter 在数据库中记录并消耗限速额度。
type Limiter struct {
	db bun.IDB
}

// NewLimiter 创建限速器。
func NewLimiter(db bun.IDB) *Limiter {
	return &Limiter{db: db}
}

// Allow 依次为各检查项消耗一次额度，遇到额度不足的检查项返回 LimitedError，其后的检查项不再消耗。
func (l *Limiter) Allow(ctx context.Context, checks ...Check) error {
	for _, check := range checks {
		if check.key == "" {
			continue
		}
		interval := check.rule.interval.Seconds()
		window := interval * float64(check.rule.burst)
		// 理论到达时间推进一个间隔后不超过当前时刻加突发窗口时放行；冲突更新在行锁内判断，并发请求按顺序消耗额度。
		var allowed bool
		if err := l.db.NewRaw(`WITH attempt AS (
	INSERT INTO rate_limits AS rl (key, tat) VALUES (?0, now() + make_interval(secs => ?1))
	ON CONFLICT (key) DO UPDATE SET tat = greatest(rl.tat, now()) + make_interval(secs => ?1)
	WHERE greatest(rl.tat, now()) + make_interval(secs => ?1) <= now() + make_interval(secs => ?2)
	RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM attempt)`, check.key, interval, window).Scan(ctx, &allowed); err != nil {
			return fmt.Errorf("check rate limit %s: %w", check.rule.name, err)
		}
		if allowed {
			continue
		}
		// 拒绝后按最新提交的理论到达时间计算恢复一次额度所需的等待。
		var retry float64
		if err := l.db.NewRaw(`SELECT extract(epoch FROM greatest(tat, now()) + make_interval(secs => ?1) - now() - make_interval(secs => ?2))::float8
FROM rate_limits WHERE key = ?0`, check.key, interval, window).Scan(ctx, &retry); err != nil {
			return fmt.Errorf("load rate limit %s: %w", check.rule.name, err)
		}
		return &LimitedError{RetryAfter: time.Duration(math.Max(math.Ceil(retry), 1)) * time.Second}
	}
	return nil
}

// IPSubject 返回按 IP 限速的对象：IPv4 取完整地址，IPv6 取所在 /64 网段，无法解析时为空。
func IPSubject(value string) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return ip.String()
	}
	return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

// Prune 删除额度已全部恢复的限速记录。
func Prune(ctx context.Context, db bun.IDB) error {
	result, err := db.NewDelete().TableExpr("rate_limits").Where("tat < now()").Exec(ctx)
	if err != nil {
		return fmt.Errorf("prune rate limits: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pruned rate limit count: %w", err)
	}
	if count > 0 {
		slog.InfoContext(ctx, "已删除额度恢复的限速记录", "count", count)
	}
	return nil
}
