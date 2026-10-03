package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// FilePurpose 表示文件上传用途。
type FilePurpose string

const (
	FilePurposeMessageAttachment FilePurpose = FilePurpose(domain.FilePurposeMessageAttachment)
	FilePurposeKnowledgeDocument FilePurpose = FilePurpose(domain.FilePurposeKnowledgeDocument)
	FilePurposeUserAvatar        FilePurpose = FilePurpose(domain.FilePurposeUserAvatar)
	FilePurposeGroupImage        FilePurpose = FilePurpose(domain.FilePurposeGroupImage)
	FilePurposeAgentAvatar       FilePurpose = FilePurpose(domain.FilePurposeAgentAvatar)
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

// ImageFile 定义原生端选择的图片文件。
type ImageFile struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	DataBase64  string `json:"dataBase64"`
}

// TextFileInput 定义原生端保存的文本文件：Name 为建议文件名，Content 为 UTF-8 文本内容。
type TextFileInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// FilePartUploadInput 定义待上传分片的序号。
type FilePartUploadInput struct {
	PartNumber int32 `json:"partNumber"`
}

// FileDownload 定义附件的即时下载地址。
type FileDownload struct {
	PreviewURL string `json:"previewUrl"`
	URL        string `json:"url"`
}
