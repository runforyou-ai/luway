package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// InvitationStatus 表示成员邀请的状态。
type InvitationStatus string

const (
	InvitationStatusPending  InvitationStatus = InvitationStatus(domain.InvitationStatusPending)
	InvitationStatusAccepted InvitationStatus = InvitationStatus(domain.InvitationStatusAccepted)
	InvitationStatusRevoked  InvitationStatus = InvitationStatus(domain.InvitationStatusRevoked)
	InvitationStatusExpired  InvitationStatus = InvitationStatus(domain.InvitationStatusExpired)
)

// InvitationInput 定义发起邀请的字段，DisplayName 为空时加入后使用账号名称。
type InvitationInput struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	RoleID      string `json:"roleId"`
}

// Invitation 定义工作区中待接受的邀请，待接受但已过期的邀请状态为 expired。
type Invitation struct {
	ID          string           `json:"id"`
	Email       string           `json:"email"`
	DisplayName string           `json:"displayName"`
	Role        RoleSummary      `json:"role"`
	Status      InvitationStatus `json:"status"`
	InviterName string           `json:"inviterName"`
	ExpiresAt   time.Time        `json:"expiresAt"`
	CreatedAt   time.Time        `json:"createdAt"`
}

// InvitationCreated 返回新邀请和只展示一次的邀请链接；EmailQueued 表示已在后台开始投递邀请邮件，投递结果不回传。
type InvitationCreated struct {
	Invitation  Invitation `json:"invitation"`
	Link        string     `json:"link"`
	EmailQueued bool       `json:"emailQueued"`
}

// InvitationList 定义当前工作区待接受的邀请。
type InvitationList struct {
	Items []Invitation `json:"items"`
}

// InvitationTokenInput 携带邀请链接中的令牌。
type InvitationTokenInput struct {
	Token string `json:"token"`
}

// InvitationPreview 定义持有邀请链接的人可以看到的邀请信息；WorkspaceSlug 供已是成员的账号直接进入工作区。
type InvitationPreview struct {
	WorkspaceName string           `json:"workspaceName"`
	WorkspaceSlug string           `json:"workspaceSlug"`
	InviterName   string           `json:"inviterName"`
	MaskedEmail   string           `json:"maskedEmail"`
	Status        InvitationStatus `json:"status"`
}
