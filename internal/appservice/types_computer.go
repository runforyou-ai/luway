package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ComputerPlatform 定义电脑的操作系统平台。
type ComputerPlatform = domain.ComputerPlatform

// ComputerKind 定义电脑的归属类型。
type ComputerKind = domain.ComputerKind

// Computer 定义个人电脑或工作区电脑：Online 表示执行器当前在线，Platform 在执行器尚未上报时为空，AgentCount 是使用该电脑的 AI 员工数。
type Computer struct {
	ID         string            `json:"id"`
	Kind       ComputerKind      `json:"kind"`
	Name       string            `json:"name"`
	Platform   *ComputerPlatform `json:"platform"`
	Online     bool              `json:"online"`
	AgentCount int               `json:"agentCount"`
	LastSeenAt *time.Time        `json:"lastSeenAt"`
	CreatedAt  time.Time         `json:"createdAt"`
	// LocalAgents 是电脑上报的可以接受委派的本机 Agent。
	LocalAgents []ComputerLocalAgent `json:"localAgents"`
}

// ComputerLocalAgent 定义电脑上一个本机 Agent 的名称与用途说明。
type ComputerLocalAgent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ComputerList 定义电脑列表。
type ComputerList struct {
	Computers []Computer `json:"computers"`
}

// ComputerRegistrationInput 定义执行器把所在电脑注册为个人电脑时上报的本机信息。
type ComputerRegistrationInput struct {
	InstallID string `json:"installId" validate:"notblank,max=64"`
	Name      string `json:"name" validate:"notblank,max=100" msg:"notblank=field.computer_name_required,max=field.computer_name_too_long"`
}

// WorkspaceComputerInput 定义添加工作区电脑时填写的名称。
type WorkspaceComputerInput struct {
	Name string `json:"name" validate:"notblank,max=100" msg:"notblank=field.computer_name_required,max=field.computer_name_too_long"`
}

// ComputerRegistration 定义注册、添加或重置凭据的结果：电脑与执行器连接服务端使用的电脑凭据，凭据只返回一次。
type ComputerRegistration struct {
	Computer   Computer `json:"computer"`
	Credential string   `json:"credential"`
}

// ComputerCapabilitiesInput 定义执行器上报的操作系统平台、执行能力、版本与同时执行的操作上限。
type ComputerCapabilitiesInput struct {
	Platform        ComputerPlatform            `json:"platform"`
	Capabilities    domain.ComputerCapabilities `json:"capabilities"`
	ExecutorVersion string                      `json:"executorVersion"`
	MaxConcurrency  int                         `json:"maxConcurrency"`
}

// ComputerClaimInput 定义执行器一次最多领取的操作数，与本机仍在执行的操作、仍持有的本机 Agent 会话和仍在等待裁决的权限请求的编号。
type ComputerClaimInput struct {
	Limit       int      `json:"limit"`
	Running     []string `json:"running"`
	Sessions    []string `json:"sessions"`
	Permissions []string `json:"permissions"`
}

// ComputerOperationItem 定义执行器领取到的一次操作；TimeoutSeconds 是执行器执行该操作的时限，到时终止操作，为零表示不设时限。
type ComputerOperationItem struct {
	ID             string                   `json:"id"`
	Operation      domain.ComputerOperation `json:"operation"`
	TimeoutSeconds int                      `json:"timeoutSeconds"`
	// TraceID 是派发到电脑时的串联编号，执行器执行该操作时沿用，派发时没有串联编号为空。
	TraceID string `json:"traceId"`
}

// ComputerOperationList 定义执行器领取到的操作，按派发顺序排列；Abort 是执行器正在执行而应当中止的操作编号，Released 是执行器持有而已释放的本机 Agent 会话编号，
// Permissions 是已有结果的权限请求。
type ComputerOperationList struct {
	Operations  []ComputerOperationItem    `json:"operations"`
	Abort       []string                   `json:"abort"`
	Released    []string                   `json:"released"`
	Permissions []ComputerPermissionResult `json:"permissions"`
}

// ComputerPermissionResult 定义一个权限请求的结果：选用的处理方式编号，为空表示请求已取消。
type ComputerPermissionResult struct {
	ID       string `json:"id"`
	OptionID string `json:"optionId"`
}

// ComputerUpdatesInput 定义执行器上报的一批过程更新。
type ComputerUpdatesInput struct {
	Updates []ComputerOperationUpdate `json:"updates"`
}

// ComputerOperationUpdate 定义一条过程更新及其序号，序号从 1 开始连续递增。
type ComputerOperationUpdate struct {
	Seq    int                   `json:"seq"`
	Update domain.ToolCallUpdate `json:"update"`
}

// ComputerPermissionInput 定义执行器上报的本机 Agent 权限请求，编号由执行器分配。
type ComputerPermissionInput struct {
	ID         string                      `json:"id"`
	Permission domain.LocalAgentPermission `json:"permission"`
}

// ComputerOutcomeInput 定义执行器上报的操作结果。
type ComputerOutcomeInput struct {
	Outcome domain.ComputerOutcome `json:"outcome"`
}

// ComputerSharedFileList 定义会话共享文件区的文件清单，按路径排序。
type ComputerSharedFileList struct {
	Files []ComputerSharedFile `json:"files"`
}

// ComputerSharedFile 定义共享文件区中的一个文件：文件区内路径、内容摘要、字节数与当前内容的下载地址，本地存储返回服务端相对路径。
type ComputerSharedFile struct {
	Path     string `json:"path"`
	Hash     string `json:"hash"`
	ByteSize int64  `json:"byteSize"`
	URL      string `json:"url"`
}

// ComputerSharedUploadInput 定义电脑为写回一个文件申请的内容上传：文件区内路径、内容类型与字节数。
type ComputerSharedUploadInput struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	ByteSize    int64  `json:"byteSize"`
}

// ComputerSharedUpload 定义创建的内容上传：内容文件编号与整体上传内容的请求，本地存储的地址为服务端相对路径。
type ComputerSharedUpload struct {
	FileID  string            `json:"fileId"`
	Request FileUploadRequest `json:"request"`
}

// ComputerSharedCommitInput 定义写回一个文件：文件区内路径、已上传的内容文件与电脑上次同步时该文件的摘要，摘要为空表示上次同步时文件不存在。
type ComputerSharedCommitInput struct {
	Path     string `json:"path"`
	FileID   string `json:"fileId"`
	BaseHash string `json:"baseHash"`
}

// ComputerSharedCommit 定义写回结果：实际写入的路径与内容摘要，文件在上次同步后已被改动或删除时路径为冲突副本。
type ComputerSharedCommit struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}
