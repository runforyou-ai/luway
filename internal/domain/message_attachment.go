package domain

// MessageAttachmentTransferStatus 表示附件内容的取回状态。
type MessageAttachmentTransferStatus string

const (
	MessageAttachmentTransferReady   MessageAttachmentTransferStatus = "ready"
	MessageAttachmentTransferPending MessageAttachmentTransferStatus = "pending"
	MessageAttachmentTransferFailed  MessageAttachmentTransferStatus = "failed"
)
