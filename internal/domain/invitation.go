package domain

import "time"

// InvitationStatus 定义工作区成员邀请的状态。
type InvitationStatus string

const (
	InvitationStatusPending  InvitationStatus = "pending"
	InvitationStatusAccepted InvitationStatus = "accepted"
	InvitationStatusRevoked  InvitationStatus = "revoked"
	InvitationStatusExpired  InvitationStatus = "expired"
)

// InvitationValidity 是邀请从创建起的有效期。
const InvitationValidity = 7 * 24 * time.Hour
