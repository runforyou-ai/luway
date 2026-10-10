package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/filex"
)

const (
	// SharedFilePrefix 是文件工具中指向会话共享文件区的路径前缀。
	SharedFilePrefix = "shared/"
	// ListSharedFilesToolName 是列出会话共享文件区文件的工具名称，提供共享文件区的运行总会注册它。
	ListSharedFilesToolName = "list_shared_files"
	// saveAttachmentToolName 是把会话中的附件保存到共享文件区的工具名称。
	saveAttachmentToolName = "save_attachment"
)

// SharedFile 是会话共享文件区中的一个文件：文件区内路径、类型、大小、内容摘要、最后修改时间与修改人名称。
type SharedFile struct {
	Path        string
	ContentType string
	ByteSize    int64
	Hash        string
	UpdatedAt   time.Time
	UpdatedBy   string
}

// SharedFileContent 是读取到的文件：Data 是不超过读取上限的原始内容，Text 是由 PDF、Word 等文档转换得到的只读文本，都为空表示内容无法按文本提供。
type SharedFileContent struct {
	SharedFile
	Data []byte
	Text string
}

// SharedFiles 是运行所属会话的共享文件区，路径是文件区内的相对路径；返回的错误原因直接交给模型。
type SharedFiles interface {
	// List 返回文件区中的全部文件，按路径排序。
	List(ctx context.Context) ([]SharedFile, error)
	// Read 读取文件的当前内容。
	Read(ctx context.Context, path string) (SharedFileContent, error)
	// Write 以完整内容创建或覆盖文件：内容已一致时直接返回，覆盖其他内容时要求 baseHash 与当前摘要一致。
	Write(ctx context.Context, path string, content []byte, baseHash string) (SharedFile, error)
	// SaveAttachment 把会话中一条附件消息的文件保存为文件区中的新文件，path 为空时使用附件名称。
	SaveAttachment(ctx context.Context, messageID, path string) (SharedFile, error)
}

// sharedPath 判断模型给出的路径是否指向会话共享文件区，并返回文件区内的相对路径。
func sharedPath(value string) (string, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")), "./")
	if value == strings.TrimSuffix(SharedFilePrefix, "/") {
		return "", true
	}
	relative, ok := strings.CutPrefix(value, SharedFilePrefix)
	return relative, ok
}

// sharedArguments 判断文件工具的调用参数是否指向会话共享文件区。
func sharedArguments(arguments string) bool {
	var input struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return false
	}
	_, shared := sharedPath(input.FilePath)
	return shared
}

// 文件工具在提供会话共享文件区时补充的说明。
const (
	sharedFileToolNote   = "\n- 以 shared/ 开头的路径指向本会话的共享文件区，成员与其他 AI 员工都能看到；用 list_shared_files 查看其中的文件。"
	sharedImageReadNote  = "模型能识别图片时，读取共享文件区中的图片后，图片随结果附给模型。"
	sharedOnlyToolNote   = "\n- 只能读写以 shared/ 开头的路径，即本会话的共享文件区，成员与其他 AI 员工都能看到；用 list_shared_files 查看其中的文件。"
	sharedReadToolDesc   = "读取文本文件，结果以 cat -n 格式带行号返回，行号从 1 开始，默认最多读取 2000 行。\n- 已知需要的范围时用 offset 与 limit 只读取该部分。\n- PDF、Word 等文档转换为文本后返回，只能读取；模型能识别图片时，图片随结果附给模型；其他二进制文件与超过 10 MB 的文件无法读取。"
	sharedWriteToolDesc  = "以完整内容创建或覆盖文件。\n- 覆盖已有文件前必须先用 read_file 读取；读取后文件被改动过时写入失败，重新读取后再写。局部修改使用 edit_file。\n- 只写入文本。"
	sharedEditToolDesc   = editFileToolDesc
	listSharedFilesDesc  = "列出本会话共享文件区中的全部文件，给出路径、大小、最后修改人与修改时间。"
	saveAttachmentDesc   = "把会话中一条附件消息的文件保存到共享文件区，之后可以用 read_file 读取。message_id 是上下文中附件的 messageId；目标路径已有文件时保存失败，换一个路径。"
	sharedNoComputerNote = "这个工具只能读写以 shared/ 开头的路径，即本会话的共享文件区。"
)

// inlineImageTypes 是能附给模型查看的图片格式。
var inlineImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

// listSharedFilesArgs 是列出共享文件工具的参数。
type listSharedFilesArgs struct{}

// saveAttachmentArgs 是保存附件工具的参数。
type saveAttachmentArgs struct {
	MessageID string `json:"message_id" jsonschema:"required" jsonschema_description:"附件所在消息的 messageId"`
	FilePath  string `json:"file_path,omitempty" jsonschema_description:"保存到的路径，以 shared/ 开头；不填时使用附件名称"`
}

// sharedFileTool 是读写会话共享文件区的文件工具：路径以 shared/ 开头时由服务端读写文件区，其余路径交给同名电脑工具，运行没有电脑时只接受文件区路径。
type sharedFileTool struct {
	info     *schema.ToolInfo
	files    SharedFiles
	images   bool          // 模型能识别图片，读到的图片附给模型。
	computer *computerTool // 运行没有电脑或授权不允许该电脑工具时为空。
	versions *fileVersions
}

// newSharedFileTool 创建读写会话共享文件区的文件工具；computer 是可用的同名电脑工具，为空时只接受共享文件区路径；images 表示模型能识别图片，读到的图片附给模型。
func newSharedFileTool(name string, files SharedFiles, versions *fileVersions, images bool, computer *computerTool) (*sharedFileTool, error) {
	var info *schema.ToolInfo
	var err error
	if computer != nil {
		copied := *computer.info
		copied.Desc += sharedFileToolNote
		if name == readFileToolName {
			copied.Desc += sharedImageReadNote
		}
		info = &copied
	} else {
		desc := map[string]string{readFileToolName: sharedReadToolDesc, writeFileToolName: sharedWriteToolDesc, editFileToolName: sharedEditToolDesc}[name] + sharedOnlyToolNote
		switch name {
		case readFileToolName:
			info, err = utils.GoStruct2ToolInfo[readFileArgs](name, desc)
		case writeFileToolName:
			info, err = utils.GoStruct2ToolInfo[writeFileArgs](name, desc)
		case editFileToolName:
			info, err = utils.GoStruct2ToolInfo[editFileArgs](name, desc)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create shared file tool %s: %w", name, err)
	}
	return &sharedFileTool{info: info, files: files, images: images, computer: computer, versions: versions}, nil
}

// newListSharedFilesTool 创建列出会话共享文件区文件的工具。
func newListSharedFilesTool(files SharedFiles) (tool.BaseTool, error) {
	list, err := utils.InferTool(ListSharedFilesToolName, listSharedFilesDesc, func(ctx context.Context, _ listSharedFilesArgs) (string, error) {
		return listSharedFiles(ctx, files)
	})
	if err != nil {
		return nil, fmt.Errorf("create list shared files tool: %w", err)
	}
	return list, nil
}

// newSaveAttachmentTool 创建把会话中的附件保存到共享文件区的工具。
func newSaveAttachmentTool(files SharedFiles) (tool.BaseTool, error) {
	save, err := utils.InferTool(saveAttachmentToolName, saveAttachmentDesc, func(ctx context.Context, input saveAttachmentArgs) (string, error) {
		relative := ""
		if input.FilePath != "" {
			var shared bool
			if relative, shared = sharedPath(input.FilePath); !shared {
				return "", errors.New("附件只能保存到以 shared/ 开头的路径。")
			}
		}
		saved, err := files.SaveAttachment(ctx, input.MessageID, relative)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已保存到 %s%s（%d 字节）。", SharedFilePrefix, saved.Path, saved.ByteSize), nil
	})
	if err != nil {
		return nil, fmt.Errorf("create save attachment tool: %w", err)
	}
	return save, nil
}

// listSharedFiles 以每行一个文件列出共享文件区：路径、大小、最后修改人与修改时间。
func listSharedFiles(ctx context.Context, files SharedFiles) (string, error) {
	entries, err := files.List(ctx)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "共享文件区中还没有文件。", nil
	}
	var text strings.Builder
	for index, entry := range entries {
		if index > 0 {
			text.WriteByte('\n')
		}
		fmt.Fprintf(&text, "%s%s\t%d 字节\t%s\t%s", SharedFilePrefix, entry.Path, entry.ByteSize, entry.UpdatedBy, entry.UpdatedAt.Format(time.RFC3339))
	}
	return text.String(), nil
}

// Info 返回模型可见的名称、描述和参数定义。
func (t *sharedFileTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 按路径执行文件操作：以 shared/ 开头的路径读写会话共享文件区，其余路径交给电脑工具。
func (t *sharedFileTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", errors.New("参数不是合法 JSON，请重新提交。")
	}
	relative, shared := sharedPath(input.FilePath)
	if !shared {
		if t.computer == nil {
			return "", errors.New(sharedNoComputerNote)
		}
		return t.computer.InvokableRun(ctx, argumentsInJSON, options...)
	}
	// 内容摘要按规范化后的文件区路径登记，同一文件的不同写法共用读取记录。
	normalized, ok := domain.NormalizeConversationFilePath(relative)
	if !ok {
		return "", errors.New("路径不合法：在 shared/ 后给出文件路径，不能包含 .. 越出共享文件区；用 list_shared_files 查看其中的文件。")
	}
	key := SharedFilePrefix + normalized
	if t.info.Name != readFileToolName && domain.BinaryDocument(normalized) {
		return "", fmt.Errorf("PDF、Word、PPT、Excel 等文档只能读取，不能用 %s 修改：%s", t.info.Name, key)
	}
	switch t.info.Name {
	case readFileToolName:
		var args readFileArgs
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", errors.New("参数不是合法 JSON，请重新提交。")
		}
		return t.read(ctx, key, normalized, args.Offset, args.Limit)
	case writeFileToolName:
		var args writeFileArgs
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", errors.New("参数不是合法 JSON，请重新提交。")
		}
		written, err := t.files.Write(ctx, normalized, []byte(args.Content), t.versions.base(key))
		if err != nil {
			return "", err
		}
		t.record(key, written)
		return fmt.Sprintf("已写入 %s%s（%d 字节）。", SharedFilePrefix, written.Path, written.ByteSize), nil
	default:
		var args editFileArgs
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", errors.New("参数不是合法 JSON，请重新提交。")
		}
		return t.edit(ctx, key, normalized, args)
	}
}

// read 读取共享文件：文本按行返回并按 key 登记内容摘要，文档返回转换后的只读文本，图片随结果附给模型，其余文件只说明无法读取。
func (t *sharedFileTool) read(ctx context.Context, key, relative string, offset, limit int) (string, error) {
	content, err := t.files.Read(ctx, relative)
	if err != nil {
		return "", err
	}
	name := SharedFilePrefix + content.Path
	switch {
	case content.Data == nil && content.ByteSize > textfile.MaxReadBytes:
		return "", fmt.Errorf("文件超过 %d MB，无法读取：%s", textfile.MaxReadBytes>>20, name)
	case domain.BinaryDocument(content.Path) && content.Text != "":
		return fmt.Sprintf("（%s 已转换为文本，只能读取，不能用 write_file 或 edit_file 修改）\n%s", name, textfile.Numbered([]byte(content.Text), offset, limit)), nil
	case domain.BinaryDocument(content.Path):
		return "", fmt.Errorf("文档没有可读取的文本，或无法解析：%s", name)
	case inlineImageTypes[content.ContentType]:
		return t.attachImage(ctx, content)
	case filex.IsBinary(content.Data):
		return "", fmt.Errorf("二进制文件（%s）无法按文本读取：%s", content.ContentType, name)
	}
	t.record(key, content.SharedFile)
	return textfile.Numbered(content.Data, offset, limit), nil
}

// attachImage 把读到的图片附给模型，模型不能识别图片或图片过大时返回原因。
func (t *sharedFileTool) attachImage(ctx context.Context, content SharedFileContent) (string, error) {
	name := SharedFilePrefix + content.Path
	if !t.images {
		return "", fmt.Errorf("当前模型不能识别图片，无法读取：%s", name)
	}
	if content.ByteSize > domain.AgentMediaMaxBytes {
		return "", fmt.Errorf("图片过大，无法读取：%s", name)
	}
	if err := einorun.AttachMedia(ctx, einorun.MediaRef{Key: agentcontract.MediaKeyShared + content.Path, MIME: content.ContentType, SHA256: content.Hash, Size: content.ByteSize}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已读取图片 %s（%s，%d 字节），图片随结果附给模型。", name, content.ContentType, content.ByteSize), nil
}

// edit 把共享文件中的一段原文替换为新内容：要求按 key 登记的读取摘要与当前内容一致，以读取到的摘要作为写入条件。
func (t *sharedFileTool) edit(ctx context.Context, key, relative string, args editFileArgs) (string, error) {
	content, err := t.files.Read(ctx, relative)
	if err != nil {
		return "", err
	}
	name := SharedFilePrefix + content.Path
	if content.Data == nil || filex.IsBinary(content.Data) {
		return "", fmt.Errorf("文件无法按文本修改：%s", name)
	}
	if err := textfile.CheckBase(name, content.Data, t.versions.base(key)); err != nil {
		return "", err
	}
	text, err := textfile.Replace(name, string(content.Data), args.OldString, args.NewString, args.ReplaceAll)
	if err != nil {
		return "", err
	}
	written, err := t.files.Write(ctx, relative, []byte(text), content.Hash)
	if err != nil {
		return "", err
	}
	t.record(key, written)
	return fmt.Sprintf("已修改 %s。", name), nil
}

// record 按规范化后的共享文件路径登记读取或写入后的内容摘要。
func (t *sharedFileTool) record(key string, file SharedFile) {
	t.versions.record(key, domain.ComputerOutcome{Path: key, Hash: file.Hash})
}
