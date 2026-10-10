//go:build server

package agent

import (
	"slices"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

const (
	ValidationBusinessSystemInvalid common.FieldCode = "AGENT_BUSINESS_SYSTEM_INVALID"
	ValidationDisplayNameRequired   common.FieldCode = "AGENT_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid    common.FieldCode = "AGENT_DISPLAY_NAME_INVALID"
	ValidationTeamInvalid           common.FieldCode = "AGENT_TEAM_INVALID"
	ValidationHandoffTeamInvalid    common.FieldCode = "AGENT_HANDOFF_TEAM_INVALID"
	ValidationResponsibleInvalid    common.FieldCode = "AGENT_RESPONSIBLE_INVALID"
	ValidationComputerInvalid       common.FieldCode = "AGENT_COMPUTER_INVALID"
	ValidationComputerGrantInvalid  common.FieldCode = "AGENT_COMPUTER_GRANT_INVALID"
	// ValidationResponsibleRequired 表示业务系统或工作区电脑的授权包含需要审批的操作，AI 员工须指定负责人。
	ValidationResponsibleRequired   common.FieldCode = "AGENT_RESPONSIBLE_REQUIRED"
	ValidationStatusInvalid         common.FieldCode = "AGENT_STATUS_INVALID"
	ValidationWorkStatusUnavailable common.FieldCode = "AGENT_WORK_STATUS_UNAVAILABLE"
	ValidationExecutionInvalid      common.FieldCode = "AGENT_EXECUTION_INVALID"
	ValidationKnowledgeBaseInvalid  common.FieldCode = "AGENT_KNOWLEDGE_BASE_INVALID"
	ValidationModelInvalid          common.FieldCode = "AGENT_MODEL_INVALID"
)

// normalizeServiceAudiences 去重并按展示顺序排列服务对象。
func normalizeServiceAudiences(values []domain.ServiceAudience) []domain.ServiceAudience {
	return arr.OrEmpty(arr.Filter(domain.AgentServiceAudiences, func(audience domain.ServiceAudience) bool { return slices.Contains(values, audience) }))
}
