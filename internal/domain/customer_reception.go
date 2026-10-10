package domain

import "time"

// CustomerReceptionReply 定义访客端展示的回复预期：immediate 为立即回复，minutes、ten_minutes、half_hour、hour 与 hours 为按工作时间计的真人首响估计的通常回复时长，soon 为尽快回复，scheduled 为下个工作时段回复，none 为不展示。
type CustomerReceptionReply string

const (
	CustomerReceptionReplyImmediate  CustomerReceptionReply = "immediate"
	CustomerReceptionReplyMinutes    CustomerReceptionReply = "minutes"
	CustomerReceptionReplyTenMinutes CustomerReceptionReply = "ten_minutes"
	CustomerReceptionReplyHalfHour   CustomerReceptionReply = "half_hour"
	CustomerReceptionReplyHour       CustomerReceptionReply = "hour"
	CustomerReceptionReplyHours      CustomerReceptionReply = "hours"
	CustomerReceptionReplySoon       CustomerReceptionReply = "soon"
	CustomerReceptionReplyScheduled  CustomerReceptionReply = "scheduled"
	CustomerReceptionReplyNone       CustomerReceptionReply = "none"
)

// CustomerReceptionReplyWithin 返回按工作时间计的真人首响中位数所在的回复时长档位：5 分钟内、10 分钟内、30 分钟内、1 小时内、4 小时内为几小时内，更长时为尽快回复。
func CustomerReceptionReplyWithin(median time.Duration) CustomerReceptionReply {
	switch {
	case median <= 5*time.Minute:
		return CustomerReceptionReplyMinutes
	case median <= 10*time.Minute:
		return CustomerReceptionReplyTenMinutes
	case median <= 30*time.Minute:
		return CustomerReceptionReplyHalfHour
	case median <= time.Hour:
		return CustomerReceptionReplyHour
	case median <= 4*time.Hour:
		return CustomerReceptionReplyHours
	default:
		return CustomerReceptionReplySoon
	}
}
