package domain

// ChatSubjectKind 定义聊天主体类型。
type ChatSubjectKind string

const (
	ChatSubjectKindWorkspaceIdentity ChatSubjectKind = "workspace_identity"
	ChatSubjectKindContact           ChatSubjectKind = "contact"
)
