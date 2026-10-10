package appservice

import "context"

// FileBackend 定义文件上传与会话共享文件区的业务调用。
type FileBackend interface {
	// CreateFileUpload 创建文件上传请求。
	//appservice:route POST /files/uploads status=201 perm=none
	CreateFileUpload(context.Context, RequestMeta, FileUploadInput) (FileUpload, error)
	// CompleteFileUpload 核验并完成文件上传。
	//appservice:route POST /files/{fileID:uuid}/complete perm=none
	CompleteFileUpload(context.Context, RequestMeta, string) (File, error)
	// CreateFilePartUpload 创建一个分片的直传请求。
	//appservice:route POST /files/{fileID:uuid}/parts perm=none
	CreateFilePartUpload(context.Context, RequestMeta, string, FilePartUploadInput) (FileUploadRequest, error)
	// CancelFileUpload 将未发送的临时文件交给清理任务。
	//appservice:route DELETE /files/{fileID:uuid}/upload perm=none
	CancelFileUpload(context.Context, RequestMeta, string) error
	// ListConversationFiles 返回当前成员可阅读会话的共享文件区中的文件，按路径排序。
	//appservice:route GET /conversations/{conversationID:uuid}/files perm=none
	ListConversationFiles(context.Context, RequestMeta, string) (ConversationFileList, error)
	// AddConversationFile 把上传完成的文件以原文件名加入会话共享文件区，同名文件已存在时在文件名后追加序号。
	//appservice:route POST /conversations/{conversationID:uuid}/files status=201 perm=none
	AddConversationFile(context.Context, RequestMeta, string, AddConversationFileInput) (ConversationFile, error)
	// SaveConversationAttachment 把会话中一条附件消息的文件保存到会话共享文件区，同名文件已存在时在文件名后追加序号。
	//appservice:route POST /conversations/{conversationID:uuid}/messages/{messageID:uuid}/attachment/save status=201 perm=none
	SaveConversationAttachment(context.Context, RequestMeta, string, string) (ConversationFile, error)
	// DeleteConversationFile 从会话共享文件区删除文件。
	//appservice:route DELETE /conversations/{conversationID:uuid}/files/{fileID:uuid} perm=none
	DeleteConversationFile(context.Context, RequestMeta, string, string) error
	// GetConversationFileDownload 签发会话共享文件区中文件的下载地址。
	//appservice:route GET /conversations/{conversationID:uuid}/files/{fileID:uuid}/download perm=none
	GetConversationFileDownload(context.Context, RequestMeta, string, string) (FileDownload, error)
}
