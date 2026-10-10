package appservice

// TeamPerformanceReportInput 定义团队表现报表的统计范围：最近 Days 天内结束的会话，ChannelID 为空表示全部渠道；PublicQueue 限定公共队列，否则 TeamID 限定团队，两者都未给出表示全部队列。
type TeamPerformanceReportInput struct {
	Days        int    `json:"days" query:"days,default=30"`
	ChannelID   string `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	TeamID      string `json:"teamId" query:"teamId" validate:"omitempty,uuid" msg:"error.team_not_found"`
	PublicQueue bool   `json:"publicQueue" query:"publicQueue"`
}

// TeamPerformanceReport 定义统计范围内已关闭周期的真人承接表现，排除小结状态为无实质诉求的周期，时长以秒计，没有样本时为空。
// HumanRequested 为需要过真人的周期数，HumanResponded 为其中有真人对客回复的周期数；首响按工作时间计，FirstResponseSampled 为有真人回复且其间经过工作时间的样本数；
// AIHandled 为由 AI 员工首接待的周期数，AI 处理时长为开启到首次需要真人或关闭；HumanHandled 为真人负责过的周期数，人工处理时长为真人首次负责到最后一次关闭；
// 评价、满意度与真人质检只统计真人负责过的周期，Reviewed 为该项已质检且适用的周期数。
type TeamPerformanceReport struct {
	Closed                    int  `json:"closed"`
	HumanRequested            int  `json:"humanRequested"`
	HumanResponded            int  `json:"humanResponded"`
	FirstResponseSampled      int  `json:"firstResponseSampled"`
	FirstResponseMedian       *int `json:"firstResponseMedian"`
	FirstResponseP90          *int `json:"firstResponseP90"`
	AIHandled                 int  `json:"aiHandled"`
	AIHandlingMedian          *int `json:"aiHandlingMedian"`
	HumanHandled              int  `json:"humanHandled"`
	HumanHandlingMedian       *int `json:"humanHandlingMedian"`
	Rated                     int  `json:"rated"`
	RatedResolved             int  `json:"ratedResolved"`
	Satisfied                 int  `json:"satisfied"`
	Neutral                   int  `json:"neutral"`
	Dissatisfied              int  `json:"dissatisfied"`
	HumanIncorrect            int  `json:"humanIncorrect"`
	HumanIncorrectReviewed    int  `json:"humanIncorrectReviewed"`
	HumanPoorAttitude         int  `json:"humanPoorAttitude"`
	HumanPoorAttitudeReviewed int  `json:"humanPoorAttitudeReviewed"`
}

// TeamPerformanceMemberListInput 定义客服表现的统计范围与分页。
type TeamPerformanceMemberListInput struct {
	Days        int    `json:"days" query:"days,default=30"`
	ChannelID   string `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	TeamID      string `json:"teamId" query:"teamId" validate:"omitempty,uuid" msg:"error.team_not_found"`
	PublicQueue bool   `json:"publicQueue" query:"publicQueue"`
	Page        int    `json:"page" query:"page,default=1"`
	PageSize    int    `json:"pageSize" query:"pageSize,default=50"`
}

// TeamPerformanceMember 定义一位客服在统计范围内关闭时由其负责的周期：Closed 为周期数，Satisfied 与 SatisfactionJudged 为其中推断满意与已判定满意度的周期数，Issues 为其中不满意、真人答错或态度问题成立的周期数。
type TeamPerformanceMember struct {
	IdentityID         string `json:"identityId"`
	DisplayName        string `json:"displayName"`
	AvatarURL          string `json:"avatarUrl"`
	Closed             int    `json:"closed"`
	Satisfied          int    `json:"satisfied"`
	SatisfactionJudged int    `json:"satisfactionJudged"`
	Issues             int    `json:"issues"`
}

// TeamPerformanceMemberList 定义一页客服表现。
type TeamPerformanceMemberList struct {
	Rows []TeamPerformanceMember `json:"rows"`
	Page PageInfo                `json:"page"`
}

// TeamPerformanceBreakdownInput 定义按维度拆分的统计范围与分页。
type TeamPerformanceBreakdownInput struct {
	Days        int                    `json:"days" query:"days,default=30"`
	ChannelID   string                 `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	TeamID      string                 `json:"teamId" query:"teamId" validate:"omitempty,uuid" msg:"error.team_not_found"`
	PublicQueue bool                   `json:"publicQueue" query:"publicQueue"`
	Dimension   ServiceReportDimension `json:"dimension" query:"dimension"`
	Page        int                    `json:"page" query:"page,default=1"`
	PageSize    int                    `json:"pageSize" query:"pageSize,default=50"`
}

// TeamPerformanceBreakdown 定义按渠道或咨询分类拆分的已关闭周期数、需要过真人的周期数与按工作时间计的真人首响中位数（秒，没有样本时为空）；ID 为空表示未分类。
type TeamPerformanceBreakdown struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Closed              int    `json:"closed"`
	HumanRequested      int    `json:"humanRequested"`
	FirstResponseMedian *int   `json:"firstResponseMedian"`
}

// TeamPerformanceBreakdownList 定义一页拆分结果。
type TeamPerformanceBreakdownList struct {
	Rows []TeamPerformanceBreakdown `json:"rows"`
	Page PageInfo                   `json:"page"`
}

// TeamPerformanceIssueListInput 定义真人接待问题会话的统计范围、问题类型与分页。
type TeamPerformanceIssueListInput struct {
	Days        int              `json:"days" query:"days,default=30"`
	ChannelID   string           `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	TeamID      string           `json:"teamId" query:"teamId" validate:"omitempty,uuid" msg:"error.team_not_found"`
	PublicQueue bool             `json:"publicQueue" query:"publicQueue"`
	Issue       ServiceIssueType `json:"issue" query:"issue,default=all"`
	Page        int              `json:"page" query:"page,default=1"`
	PageSize    int              `json:"pageSize" query:"pageSize,default=50"`
}
