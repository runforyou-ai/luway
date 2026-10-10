package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// FilePurpose 表示文件上传用途。
type FilePurpose string

const (
	FilePurposeMessageAttachment FilePurpose = FilePurpose(domain.FilePurposeMessageAttachment)
	FilePurposeKnowledgeDocument FilePurpose = FilePurpose(domain.FilePurposeKnowledgeDocument)
	FilePurposeUserAvatar        FilePurpose = FilePurpose(domain.FilePurposeUserAvatar)
	FilePurposeGroupImage        FilePurpose = FilePurpose(domain.FilePurposeGroupImage)
	FilePurposeAgentAvatar       FilePurpose = FilePurpose(domain.FilePurposeAgentAvatar)
	FilePurposeConversationFile  FilePurpose = FilePurpose(domain.FilePurposeConversationFile)
)

// FileUploadInput 定义创建上传所需的文件元数据。
type FileUploadInput struct {
	Purpose     FilePurpose `json:"purpose"`
	FileName    string      `json:"fileName"`
	ContentType string      `json:"contentType"`
	ByteSize    int64       `json:"byteSize"`
}

// File 定义前端可使用的文件元数据。
type File struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	ByteSize    int64  `json:"byteSize"`
	ContentURL  string `json:"contentUrl"`
}

// FileUploadRequest 定义客户端上传文件内容所需的 HTTP 请求。
type FileUploadRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// FileUpload 包含待上传文件和内容上传请求。
type FileUpload struct {
	PartSize int64             `json:"partSize"`
	File     File              `json:"file"`
	Request  FileUploadRequest `json:"request"`
}

// FilePartUploadInput 定义待上传分片的序号。
type FilePartUploadInput struct {
	PartNumber int32 `json:"partNumber"`
}

// FileDownload 定义附件的即时下载地址。
type FileDownload struct {
	// PreviewURL 只对浏览器可内嵌展示的图片返回。
	PreviewURL string `json:"previewUrl"`
	URL        string `json:"url"`
}

// ConversationFile 定义会话共享文件区中的一个文件：路径、当前版本的类型与大小、最后修改人与修改时间。
type ConversationFile struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	ContentType string    `json:"contentType"`
	ByteSize    int64     `json:"byteSize"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// UpdatedByName 是最后修改人的名称，主体已不存在时为空。
	UpdatedByName string `json:"updatedByName"`
}

// ConversationFileList 定义会话共享文件区中的全部文件，按路径排序。
type ConversationFileList struct {
	Items []ConversationFile `json:"items"`
}

// AddConversationFileInput 定义加入会话共享文件区的已上传文件。
type AddConversationFileInput struct {
	FileID string `json:"fileId" validate:"uuid" msg:"error.file_not_found"`
}
