//go:build server

package aiperformance

import (
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// reportScopeSQL 定义报表的两个公共集合，第一个 %s 处拼入限定 ss.organization_id 的工作区条件，第二个 %s 处拼入可选的渠道与 AI 员工条件。
// closed 为统计范围内由 AI 员工接待过的已关闭周期，排除小结状态为无实质诉求的周期；resolved 取小结的是否解决，为空记为未判定；
// ai_only 按 messagequery.AIOnly 判断周期由 AI 独立处理；质检字段取周期质检结果，未质检时为空。
// handoffs 为这些周期内的转人工事件：AI 主动转人工取事件记录的原因，AI 员工负责时被退回队列记为 AI 员工不可用。
const reportScopeSQL = `
WITH closed AS (
	SELECT ss.id, ss.organization_id, ss.conversation_id, ss.closed_at, ss.close_reason, ss.category_id, ss.rating_resolved, ss.resolved, cci.channel_id,
		ssr.satisfaction, ssr.ai_incorrect, ssr.ai_missed_handoff, ssr.ai_poor_attitude,
		? AS ai_only
	FROM service_sessions ss
	LEFT JOIN channel_conversations cc ON cc.conversation_id = ss.conversation_id AND cc.organization_id = ss.organization_id
	LEFT JOIN contact_channel_identities cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id
	LEFT JOIN service_session_reviews ssr ON ssr.organization_id = ss.organization_id AND ssr.service_session_id = ss.id
	WHERE %s AND ss.status = ? AND ss.summary_status IS DISTINCT FROM ? AND ss.agent_identity_id IS NOT NULL
		AND ss.closed_at >= now() - make_interval(days => ?)%s
),
handoffs AS (
	SELECT m.id, m.organization_id, m.conversation_id, m.service_session_id, m.message_seq, m.created_at AS occurred_at,
		CASE WHEN m.system_event_type = ? THEN m.system_event_payload->>'reason' ELSE ? END AS reason
	FROM closed
	JOIN messages m ON m.organization_id = closed.organization_id AND m.service_session_id = closed.id
	LEFT JOIN organization_identities oi ON oi.organization_id = m.organization_id
		AND m.system_event_type = ? AND oi.id = (m.system_event_payload->>'fromIdentityId')::uuid
	WHERE m.system_event_type = ? OR (m.system_event_type = ? AND oi.type = ?)
)`

// reportScope 返回单个工作区拼好渠道与 AI 员工条件的公共集合 SQL 与参数，调用方在其后追加本条查询。
func reportScope(identity *servermodels.Identity, input Input) (string, []any) {
	// 指定渠道时追加渠道条件，全部渠道时不比较。
	filters, args := "", []any{}
	if input.ChannelID != "" {
		filters = " AND cci.channel_id = ?"
		args = append(args, input.ChannelID)
	}
	if condition, conditionArgs := input.Agents.Condition("ss.agent_identity_id", identity.Organization.ID); condition != "" {
		filters += " AND " + condition
		args = append(args, conditionArgs...)
	}
	return scopeSQL("ss.organization_id = ?", []any{identity.Organization.ID}, input.Days, filters, args)
}

// scopeSQL 返回公共集合 SQL 与参数：organizations 为限定 ss.organization_id 的工作区条件，filters 为追加在周期上的条件。
func scopeSQL(organizations string, organizationArgs []any, days int, filters string, filterArgs []any) (string, []any) {
	handedOff, returned := domain.ConversationSystemEventServiceSessionHandedOff, domain.ConversationSystemEventServiceSessionReturned
	args := slices.Concat([]any{messagequery.AIOnly("ss")}, organizationArgs,
		[]any{domain.ServiceSessionStatusClosed, domain.ServiceSessionSummaryNoRequest, days}, filterArgs,
		[]any{handedOff, domain.AgentHandoffReasonAgentUnavailable, returned, handedOff, returned, domain.OrganizationIdentityTypeAgent})
	return fmt.Sprintf(reportScopeSQL, organizations, filters), args
}
