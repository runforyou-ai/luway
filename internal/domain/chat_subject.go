package domain

// ChatSubjectKind 定义聊天主体类型。
type ChatSubjectKind string

const (
	ChatSubjectKindOrganizationIdentity ChatSubjectKind = "organization_identity"
	ChatSubjectKindContact              ChatSubjectKind = "contact"
)
