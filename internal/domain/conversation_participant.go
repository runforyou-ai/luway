package domain

// ConversationParticipantRole 定义会话参与角色。
type ConversationParticipantRole string

const (
	ConversationParticipantRoleOwner  ConversationParticipantRole = "owner"
	ConversationParticipantRoleMember ConversationParticipantRole = "member"
)
