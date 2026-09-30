//go:build server

// Package aiperformance 汇总 AI 员工接待过的客服周期的表现：解决率、结束方式、客户评价、推断满意度、AI 质检、转人工原因、按渠道与咨询分类的拆分、问题会话和待处理的待补知识条数，以及 AI 员工接待的服务记录；待补知识条数不受统计天数限制。
package aiperformance

import (
	"errors"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
)

var (
	// ErrPageSizeInvalid 表示每页数量超出上限。
	ErrPageSizeInvalid = errors.New("ai performance page size invalid")
	// ErrDimensionInvalid 表示拆分维度不是渠道或咨询分类。
	ErrDimensionInvalid = errors.New("ai performance dimension invalid")
	// ErrIssueInvalid 表示问题会话的筛选类型不适用于 AI 表现。
	ErrIssueInvalid = errors.New("ai performance issue invalid")
)

// Input 定义报表统计范围：最近 Days 天内关闭、由 AI 员工接待过的周期，ChannelID 为空表示全部渠道，Agents 限定周期的接待 AI 员工。
type Input struct {
	Days      int
	ChannelID string
	Agents    identityaction.AgentScope
}

// Summary 定义统计范围内 AI 员工接待过的已关闭周期的整体计数，排除小结状态为无实质诉求的周期：Resolved 与 Unresolved 按小结的是否解决计数，其余为未判定；AIOnly 为 AI 员工独立处理并关闭的周期数，AIResolved 与 AIUnresolved 为其中的已解决与未解决数，HandedOff 为发生过转人工的周期数；Satisfied、Neutral 与 Dissatisfied 按推断满意度计数，其余为未判定；AIIncorrect 等为质检标记成立的周期数，对应的 Reviewed 为该项已质检且适用的周期数。
type Summary struct {
	Closed                  int `bun:"closed"`
	Resolved                int `bun:"resolved"`
	Unresolved              int `bun:"unresolved"`
	AIOnly                  int `bun:"ai_only"`
	AIResolved              int `bun:"ai_resolved"`
	AIUnresolved            int `bun:"ai_unresolved"`
	HandedOff               int `bun:"handed_off"`
	CloseAIResolved         int `bun:"close_ai_resolved"`
	CustomerUnresponsive    int `bun:"customer_unresponsive"`
	Manual                  int `bun:"manual"`
	Rated                   int `bun:"rated"`
	RatedResolved           int `bun:"rated_resolved"`
	Satisfied               int `bun:"satisfied"`
	Neutral                 int `bun:"neutral"`
	Dissatisfied            int `bun:"dissatisfied"`
	AIIncorrect             int `bun:"ai_incorrect"`
	AIIncorrectReviewed     int `bun:"ai_incorrect_reviewed"`
	AIMissedHandoff         int `bun:"ai_missed_handoff"`
	AIMissedHandoffReviewed int `bun:"ai_missed_handoff_reviewed"`
	AIPoorAttitude          int `bun:"ai_poor_attitude"`
	AIPoorAttitudeReviewed  int `bun:"ai_poor_attitude_reviewed"`
}

// ReasonCount 定义一种转人工原因的出现次数。
type ReasonCount struct {
	Reason string `bun:"reason" json:"reason"`
	Count  int    `bun:"count" json:"count"`
}

// Overview 定义报表概览：整体计数、转人工原因分布与所选渠道和 AI 员工范围下全部待处理的待补知识条数。
type Overview struct {
	Summary           Summary
	HandoffReasons    []ReasonCount
	KnowledgeGapTotal int
}

// BreakdownInput 定义按维度拆分的统计范围与分页。
type BreakdownInput struct {
	Input
	Dimension domain.ServiceReportDimension
	Page      int
	PageSize  int
}

// Breakdown 定义按渠道或咨询分类拆分的已关闭周期数、已解决数与 AI 独立解决数；ID 为空表示未分类。
type Breakdown struct {
	ID         *string `bun:"id" json:"id"`
	Name       string  `bun:"name" json:"name"`
	Closed     int     `bun:"closed" json:"closed"`
	Resolved   int     `bun:"resolved" json:"resolved"`
	AIResolved int     `bun:"ai_resolved" json:"ai_resolved"`
}

// BreakdownList 定义一页拆分结果与总行数。
type BreakdownList struct {
	Rows     []Breakdown
	Page     int
	PageSize int
	Total    int
}
