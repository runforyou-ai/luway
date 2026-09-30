//go:build server

// Package teamperformance 汇总真人客服表现：按工作时间计的真人首响、AI 与人工处理时长、真人接待的满意度与质检，按客服、渠道与咨询分类的拆分，以及真人接待的问题会话。
package teamperformance

import (
	"errors"

	"github.com/runforyou-ai/cervi/internal/domain"
)

var (
	// ErrPageSizeInvalid 表示每页数量超出上限。
	ErrPageSizeInvalid = errors.New("team performance page size invalid")
	// ErrDimensionInvalid 表示拆分维度不是渠道或咨询分类。
	ErrDimensionInvalid = errors.New("team performance dimension invalid")
	// ErrIssueInvalid 表示问题会话的筛选类型不适用于团队表现。
	ErrIssueInvalid = errors.New("team performance issue invalid")
)

// Input 定义报表统计范围：最近 Days 天内关闭的周期，ChannelID 为空表示全部渠道；PublicQueue 限定公共队列，否则 TeamID 限定团队，两者都未给出表示全部队列。
type Input struct {
	Days        int
	ChannelID   string
	TeamID      string
	PublicQueue bool
}

// Summary 定义统计范围内已关闭周期的整体计数与时长，排除小结状态为无实质诉求的周期，时长以秒计，没有样本时为空。
// HumanRequested 为需要过真人的周期数，HumanResponded 为其中有真人对客回复的周期数；首响按工作时间计，FirstResponseSampled 为有真人回复且其间经过工作时间的样本数；
// AIHandled 为由 AI 员工首接待的周期数，AI 处理时长为开启到首次需要真人或关闭；HumanHandled 为真人负责过的周期数，人工处理时长为真人首次负责到最后一次关闭；
// 评价、满意度与真人质检只统计真人负责过的周期，Reviewed 为该项已质检且适用的周期数。
type Summary struct {
	Closed                    int  `bun:"closed"`
	HumanRequested            int  `bun:"human_requested"`
	HumanResponded            int  `bun:"human_responded"`
	FirstResponseSampled      int  `bun:"first_response_sampled"`
	FirstResponseMedian       *int `bun:"first_response_median"`
	FirstResponseP90          *int `bun:"first_response_p90"`
	AIHandled                 int  `bun:"ai_handled"`
	AIHandlingMedian          *int `bun:"ai_handling_median"`
	HumanHandled              int  `bun:"human_handled"`
	HumanHandlingMedian       *int `bun:"human_handling_median"`
	Rated                     int  `bun:"rated"`
	RatedResolved             int  `bun:"rated_resolved"`
	Satisfied                 int  `bun:"satisfied"`
	Neutral                   int  `bun:"neutral"`
	Dissatisfied              int  `bun:"dissatisfied"`
	HumanIncorrect            int  `bun:"human_incorrect"`
	HumanIncorrectReviewed    int  `bun:"human_incorrect_reviewed"`
	HumanPoorAttitude         int  `bun:"human_poor_attitude"`
	HumanPoorAttitudeReviewed int  `bun:"human_poor_attitude_reviewed"`
}

// ListInput 定义分页列表的统计范围与分页。
type ListInput struct {
	Input
	Page     int
	PageSize int
}

// Member 定义一位客服在统计范围内关闭时由其负责的周期：Closed 为周期数，Satisfied 与 SatisfactionJudged 为其中推断满意与已判定满意度的周期数，Issues 为其中不满意、真人答错或态度问题成立的周期数。
type Member struct {
	IdentityID         string  `json:"id"`
	DisplayName        string  `json:"display_name"`
	AvatarFileID       *string `json:"avatar_file_id"`
	Closed             int     `json:"closed"`
	Satisfied          int     `json:"satisfied"`
	SatisfactionJudged int     `json:"satisfaction_judged"`
	Issues             int     `json:"issues"`
}

// MemberList 定义一页客服表现与总行数。
type MemberList struct {
	Rows     []Member
	Page     int
	PageSize int
	Total    int
}

// BreakdownInput 定义按维度拆分的统计范围与分页。
type BreakdownInput struct {
	ListInput
	Dimension domain.ServiceReportDimension
}

// Breakdown 定义按渠道或咨询分类拆分的已关闭周期数、需要过真人的周期数与按工作时间计的真人首响中位数（秒，没有样本时为空）；ID 为空表示未分类。
type Breakdown struct {
	ID                  *string `bun:"id" json:"id"`
	Name                string  `bun:"name" json:"name"`
	Closed              int     `bun:"closed" json:"closed"`
	HumanRequested      int     `bun:"human_requested" json:"human_requested"`
	FirstResponseMedian *int    `bun:"first_response_median" json:"first_response_median"`
}

// BreakdownList 定义一页拆分结果与总行数。
type BreakdownList struct {
	Rows     []Breakdown
	Page     int
	PageSize int
	Total    int
}

// IssueListInput 定义问题会话的统计范围、问题类型与分页。
type IssueListInput struct {
	ListInput
	Issue domain.ServiceIssueType
}
