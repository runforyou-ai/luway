package domain

// FileStorageBackend 定义文件内容实际保存的位置。
type FileStorageBackend string

const (
	FileStorageBackendLocal FileStorageBackend = "local"
	FileStorageBackendS3    FileStorageBackend = "s3"
)

// LocalFilePublicPath 是本地存储文件相对服务端的公开路径前缀。
const LocalFilePublicPath = "/storage"

// FilePurpose 定义文件上传用途。
type FilePurpose string

const (
	FilePurposeMessageAttachment FilePurpose = "message_attachment"
	FilePurposeKnowledgeDocument FilePurpose = "knowledge_document"
	FilePurposeUserAvatar        FilePurpose = "user_avatar"
	FilePurposeContactAvatar     FilePurpose = "contact_avatar"
	FilePurposeGroupImage        FilePurpose = "group_image"
	FilePurposeAgentAvatar       FilePurpose = "agent_avatar"
)

// FileStatus 定义文件生命周期状态。
type FileStatus string

const (
	FileStatusPending  FileStatus = "pending"
	FileStatusUploaded FileStatus = "uploaded"
	FileStatusActive   FileStatus = "active"
	FileStatusDeleting FileStatus = "deleting"
)

// FilePartSize 是分片上传阈值和默认分片字节数。
const FilePartSize int64 = 5 * 1024 * 1024
