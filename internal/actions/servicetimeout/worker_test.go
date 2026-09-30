//go:build server

package servicetimeout

import (
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestDueAction 验证提醒、回收、队列提醒与 AI 跟进、AI 关单的到期判断。
func TestDueAction(t *testing.T) {
	now := time.Now()
	timeouts := domain.ServiceTimeouts{ResponseReminderMinutes: 5, ResponseReclaimMinutes: 15, QueueReminderMinutes: 5, AIFollowUpMinutes: 10, AICloseMinutes: 30}
	ago := func(minutes int) *time.Time {
		value := now.Add(-time.Duration(minutes) * time.Minute)
		return &value
	}
	assignee := "assignee"
	open := string(domain.ServiceSessionStatusOpen)
	cases := []struct {
		name         string
		session      servermodels.ServiceSession
		assigneeType domain.OrganizationIdentityType
		agentIdle    bool
		want         timeoutAction
	}{
		{"未到提醒", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(4), AssigneeAssignedAt: ago(4)}, domain.OrganizationIdentityTypeUser, false, actionNone},
		{"提醒负责人", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(6), AssigneeAssignedAt: ago(6)}, domain.OrganizationIdentityTypeUser, false, actionRemindAssignee},
		{"已提醒", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(6), AssigneeAssignedAt: ago(6), RemindedAt: ago(1)}, domain.OrganizationIdentityTypeUser, false, actionNone},
		{"接手时间较晚", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(20), AssigneeAssignedAt: ago(2)}, domain.OrganizationIdentityTypeUser, false, actionNone},
		{"回收", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(16), AssigneeAssignedAt: ago(16), RemindedAt: ago(10)}, domain.OrganizationIdentityTypeUser, false, actionReclaim},
		{"AI 员工负责且客户等待回复", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AwaitingReplySince: ago(30), AssigneeAssignedAt: ago(30), LastMessageAt: *ago(30)}, domain.OrganizationIdentityTypeAgent, false, actionNone},
		{"AI 未到跟进", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(20), LastMessageAt: *ago(9)}, domain.OrganizationIdentityTypeAgent, true, actionNone},
		{"AI 跟进", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(20), LastMessageAt: *ago(10)}, domain.OrganizationIdentityTypeAgent, true, actionFollowUp},
		{"AI 最后发言不是本人或运行在途", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, LastMessageAt: *ago(60)}, domain.OrganizationIdentityTypeAgent, false, actionNone},
		{"AI 确认请求未到关单", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, LastMessageAt: *ago(29), ResolutionRequestedAt: ago(29)}, domain.OrganizationIdentityTypeAgent, true, actionNone},
		{"AI 确认请求后关单", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(40), LastMessageAt: *ago(30), ResolutionRequestedAt: ago(30)}, domain.OrganizationIdentityTypeAgent, true, actionCloseUnresponsive},
		{"AI 跟进从较晚的接手时间起算", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(5), LastMessageAt: *ago(60)}, domain.OrganizationIdentityTypeAgent, true, actionNone},
		{"接手前的确认请求不计入关单", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(5), LastMessageAt: *ago(60), ResolutionRequestedAt: ago(60)}, domain.OrganizationIdentityTypeAgent, true, actionNone},
		{"接手前的确认请求按跟进计时", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(15), LastMessageAt: *ago(60), ResolutionRequestedAt: ago(60)}, domain.OrganizationIdentityTypeAgent, true, actionFollowUp},
		{"AI 关单从较晚的确认时间起算", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, LastMessageAt: *ago(40), ResolutionRequestedAt: ago(20)}, domain.OrganizationIdentityTypeAgent, true, actionNone},
		{"没有等待", servermodels.ServiceSession{Status: open, AssigneeIdentityID: &assignee, AssigneeAssignedAt: ago(30)}, domain.OrganizationIdentityTypeUser, false, actionNone},
		{"队列提醒", servermodels.ServiceSession{Status: open, AwaitingReplySince: ago(6)}, "", false, actionRemindQueue},
		{"队列已提醒", servermodels.ServiceSession{Status: open, AwaitingReplySince: ago(30), RemindedAt: ago(1)}, "", false, actionNone},
		{"已关闭", servermodels.ServiceSession{Status: string(domain.ServiceSessionStatusClosed), AwaitingReplySince: ago(30)}, "", false, actionNone},
	}
	for _, test := range cases {
		if got := dueAction(&test.session, test.assigneeType, test.agentIdle, timeouts, now); got != test.want {
			t.Errorf("%s: got %d, want %d", test.name, got, test.want)
		}
	}
}
