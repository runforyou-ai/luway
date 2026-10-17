//go:build server

package reportpage

import "github.com/runforyou-ai/luway/internal/domain"

// ServiceFilters 定义服务报表在周期上的公共筛选：服务对象、来源与渠道；空值表示不限定该项。
type ServiceFilters struct {
	Audience      domain.ServiceAudience
	ServiceSource domain.ServiceSource
	ChannelID     string
}

// AppendClosedSessionFilters 把服务对象、来源与渠道条件追加到已关闭周期筛选（依赖 svc 与 ss.channel_id），返回追加后的 filters 与参数。
func (f ServiceFilters) AppendClosedSessionFilters(filters string, args []any) (string, []any) {
	if f.Audience != "" {
		filters += " AND svc.audience = ?"
		args = append(args, f.Audience)
	}
	if f.ServiceSource != "" {
		filters += " AND svc.source = ?"
		args = append(args, f.ServiceSource)
	}
	if f.ChannelID != "" {
		filters += " AND ss.channel_id = ?"
		args = append(args, f.ChannelID)
	}
	return filters, args
}
