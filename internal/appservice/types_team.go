package appservice

import "github.com/runforyou-ai/luway/internal/apptypes"

// TeamSummary 定义团队选择项和成员所属团队字段。
type TeamSummary = apptypes.TeamSummary

// TeamInput 定义团队可编辑字段。
type TeamInput = apptypes.TeamInput

// Team 定义团队详情。
type Team = apptypes.Team

// TeamListInput 定义团队列表查询条件。
type TeamListInput = apptypes.TeamListInput

// TeamList 定义团队分页结果。
type TeamList = apptypes.TeamList

// TeamMemberListInput 定义团队成员列表查询条件。
type TeamMemberListInput = apptypes.TeamMemberListInput

// TeamMember 定义团队成员信息。UserID 与 AgentID 按身份类型二选一，另一个为空字符串。
type TeamMember = apptypes.TeamMember

// TeamMemberList 定义团队成员分页结果。
type TeamMemberList = apptypes.TeamMemberList

// TeamMemberCandidateInput 定义可加入团队的成员查询条件。
type TeamMemberCandidateInput = apptypes.TeamMemberCandidateInput

// TeamMemberCandidate 定义可加入团队的成员。
type TeamMemberCandidate = apptypes.TeamMemberCandidate

// TeamMemberCandidateList 定义可加入团队的成员分页结果。
type TeamMemberCandidateList = apptypes.TeamMemberCandidateList

// TeamMemberIdentityInput 定义要变更的团队成员身份。
type TeamMemberIdentityInput = apptypes.TeamMemberIdentityInput

// TeamMemberInput 定义批量变更团队的身份列表。
type TeamMemberInput = apptypes.TeamMemberInput
