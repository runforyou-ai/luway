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
	// FilePurposeConversationFile 是会话共享文件区中文件的内容。
	FilePurposeConversationFile FilePurpose = "conversation_file"
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

// inlineImageExtensions 列出浏览器可内嵌展示的图片内容类型及其存储扩展名，其余文件只按附件下载。
var inlineImageExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// InlineImageExtension 返回可内嵌展示图片的存储扩展名，内容类型不可内嵌展示时返回 false。
func InlineImageExtension(contentType string) (string, bool) {
	extension, ok := inlineImageExtensions[contentType]
	return extension, ok
}

// InlineImageContentType 返回存储扩展名对应的可内嵌展示图片内容类型，扩展名不可内嵌展示时返回 false。
func InlineImageContentType(extension string) (string, bool) {
	for contentType, value := range inlineImageExtensions {
		if value == extension {
			return contentType, true
		}
	}
	return "", false
}
