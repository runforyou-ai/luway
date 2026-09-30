//go:build server

package teamperformance

import (
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// reportScopeSQL 定义报表的公共集合 closed，%s 处拼入可选的渠道与队列条件。
// closed 为统计范围内已关闭的周期，排除小结状态为无实质诉求的周期；member_id 为关闭时的真人负责人，负责人不是真人或周期从未由真人负责时为空；
// first_response_seconds 为按工作时间计的真人首响，只在有真人回复且其间经过工作时间时有值；ai_handling_seconds 只在 AI 员工首接待时有值，为开启到首次需要真人或关闭；
// human_handling_seconds 只在真人负责过时有值，为真人首次负责到最后一次关闭。
const reportScopeSQL = `
WITH closed AS (
	SELECT ss.id, ss.organization_id, ss.closed_at, ss.category_id, cci.channel_id, ss.rating_resolved,
		ss.human_requested_at, ss.human_assigned_at, ss.human_first_response_at,
		CASE WHEN ss.human_assigned_at IS NOT NULL THEN assignee.id END AS member_id,
		ss.human_first_response_seconds AS first_response_seconds,
		CASE WHEN ss.agent_identity_id IS NOT NULL AND (ss.human_requested_at IS NULL OR ss.human_requested_at > ss.created_at)
			THEN extract(epoch FROM coalesce(ss.human_requested_at, ss.closed_at) - ss.created_at) END AS ai_handling_seconds,
		extract(epoch FROM ss.closed_at - ss.human_assigned_at) AS human_handling_seconds,
		ssr.satisfaction, ssr.human_incorrect, ssr.human_poor_attitude
	FROM service_sessions ss
	LEFT JOIN organization_identities assignee ON assignee.organization_id = ss.organization_id AND assignee.id = ss.assignee_identity_id AND assignee.type = ?
	LEFT JOIN channel_conversations cc ON cc.conversation_id = ss.conversation_id AND cc.organization_id = ss.organization_id
	LEFT JOIN contact_channel_identities cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id
	LEFT JOIN service_session_reviews ssr ON ssr.organization_id = ss.organization_id AND ssr.service_session_id = ss.id
	WHERE ss.organization_id = ? AND ss.status = ? AND ss.summary_status IS DISTINCT FROM ?
		AND ss.closed_at >= now() - make_interval(days => ?)%s
)`

// median 与 p90 按秒汇总时长列，四舍五入为整数秒，没有样本时为空。
const (
	median = "round(percentile_cont(0.5) WITHIN GROUP (ORDER BY %s))::int"
	p90    = "round(percentile_cont(0.9) WITHIN GROUP (ORDER BY %s))::int"
)

// reportScope 返回拼好渠道与队列条件的公共集合 SQL 与参数，调用方在其后追加本条查询；选定公共队列时忽略团队。
func reportScope(identity *servermodels.Identity, input Input) (string, []any) {
	args := []any{
		domain.OrganizationIdentityTypeUser,
		identity.Organization.ID, domain.ServiceSessionStatusClosed, domain.ServiceSessionSummaryNoRequest, input.Days,
	}
	// 指定渠道或队列时追加对应条件，全部时不比较。
	filters := ""
	if input.ChannelID != "" {
		filters += " AND cci.channel_id = ?"
		args = append(args, input.ChannelID)
	}
	if input.PublicQueue {
		filters += " AND ss.team_id IS NULL"
	} else if input.TeamID != "" {
		filters += " AND ss.team_id = ?"
		args = append(args, input.TeamID)
	}
	return fmt.Sprintf(reportScopeSQL, filters), args
}
