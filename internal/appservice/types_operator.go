//go:build server

package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// OperatorRequestMeta 描述运营调用的服务凭据、请求关联标识和语言。
type OperatorRequestMeta struct {
	Credential string
	RequestID  string
	Locale     Locale
}

// OperatorIdentity 表示已通过凭据校验的运营调用方。
type OperatorIdentity struct {
	RequestID string
}

// OperatorDeployment 描述本部署的形态与部署地址。
type OperatorDeployment struct {
	Mode      DeploymentMode `json:"mode"`
	PublicURL string         `json:"publicUrl"`
}

// OrganizationLifecycleStatus 表示工作区生命周期状态。
type OrganizationLifecycleStatus string

const (
	OrganizationLifecycleActive    OrganizationLifecycleStatus = OrganizationLifecycleStatus(domain.OrganizationLifecycleActive)
	OrganizationLifecycleSuspended OrganizationLifecycleStatus = OrganizationLifecycleStatus(domain.OrganizationLifecycleSuspended)
	OrganizationLifecycleDeleting  OrganizationLifecycleStatus = OrganizationLifecycleStatus(domain.OrganizationLifecycleDeleting)
	OrganizationLifecycleDeleted   OrganizationLifecycleStatus = OrganizationLifecycleStatus(domain.OrganizationLifecycleDeleted)
)

// OperatorOrganizationListInput 定义运营工作区列表的筛选与分页条件，Query 匹配工作区名称或标识。
type OperatorOrganizationListInput struct {
	Query           string                       `json:"query" query:"query"`
	LifecycleStatus *OrganizationLifecycleStatus `json:"lifecycleStatus,omitempty" query:"lifecycleStatus"`
	Page            int                          `json:"page" query:"page,default=1"`
	PageSize        int                          `json:"pageSize" query:"pageSize,default=50"`
}

// OperatorEntitlement 描述工作区当前生效的服务权益，Valid 由服务截止时间计算。
type OperatorEntitlement struct {
	Revision      int64      `json:"revision"`
	PlanCode      string     `json:"planCode"`
	ServiceEndsAt *time.Time `json:"serviceEndsAt"`
	Valid         bool       `json:"valid"`
	AppliedAt     time.Time  `json:"appliedAt"`
}

// OperatorOrganization 描述运营侧查看的工作区摘要；没有权益快照的工作区 Entitlement 为空。
type OperatorOrganization struct {
	ID              string                      `json:"id"`
	Name            string                      `json:"name"`
	Slug            string                      `json:"slug"`
	PublicURL       string                      `json:"publicUrl"`
	LifecycleStatus OrganizationLifecycleStatus `json:"lifecycleStatus"`
	Entitlement     *OperatorEntitlement        `json:"entitlement"`
	CreatedAt       time.Time                   `json:"createdAt"`
}

// OperatorOrganizationList 是一页工作区摘要及符合条件的总数。
type OperatorOrganizationList struct {
	Items []OperatorOrganization `json:"items"`
	Total int                    `json:"total"`
}
