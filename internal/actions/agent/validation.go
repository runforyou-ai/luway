//go:build server

package agent

import (
	"slices"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	ValidationMCPServerInvalid         common.FieldCode = "AGENT_MCP_SERVER_INVALID"
	ValidationDisplayNameRequired      common.FieldCode = "AGENT_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid       common.FieldCode = "AGENT_DISPLAY_NAME_INVALID"
	ValidationTeamInvalid              common.FieldCode = "AGENT_TEAM_INVALID"
	ValidationServiceAudienceInvalid   common.FieldCode = "AGENT_SERVICE_AUDIENCE_INVALID"
	ValidationHandoffTeamInvalid       common.FieldCode = "AGENT_HANDOFF_TEAM_INVALID"
	ValidationResponsibleInvalid       common.FieldCode = "AGENT_RESPONSIBLE_INVALID"
	ValidationStatusInvalid            common.FieldCode = "AGENT_STATUS_INVALID"
	ValidationWorkStatusInvalid        common.FieldCode = "AGENT_WORK_STATUS_INVALID"
	ValidationWorkStatusUnavailable    common.FieldCode = "AGENT_WORK_STATUS_UNAVAILABLE"
	ValidationExecutionInvalid         common.FieldCode = "AGENT_EXECUTION_INVALID"
	ValidationKnowledgeBaseInvalid     common.FieldCode = "AGENT_KNOWLEDGE_BASE_INVALID"
	ValidationModelInvalid             common.FieldCode = "AGENT_MODEL_INVALID"
	ValidationSystemInstructionTooLong common.FieldCode = "AGENT_SYSTEM_INSTRUCTION_TOO_LONG"
	ValidationLocalAgentInvalid        common.FieldCode = "AGENT_LOCAL_AGENT_INVALID"
	ValidationLocalAgentUnavailable    common.FieldCode = "AGENT_LOCAL_AGENT_UNAVAILABLE"
)

// normalizeServiceAudiences 去重并按展示顺序排列服务对象，包含 AI 员工不可选择的服务对象时返回字段错误。
func normalizeServiceAudiences(values []domain.ServiceAudience) ([]domain.ServiceAudience, error) {
	for _, value := range values {
		if !slices.Contains(domain.AgentServiceAudiences, value) {
			return nil, &common.FieldError{Fields: map[string]common.FieldCode{"serviceAudiences": ValidationServiceAudienceInvalid}}
		}
	}
	audiences := make([]domain.ServiceAudience, 0, len(values))
	for _, audience := range domain.AgentServiceAudiences {
		if slices.Contains(values, audience) {
			audiences = append(audiences, audience)
		}
	}
	return audiences, nil
}
