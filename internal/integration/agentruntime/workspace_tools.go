package agentruntime

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	fsmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const lsToolDesc = `列出目录的直接子项，目录以路径分隔符结尾。
- path 为空时列出本会话的默认文件夹；相对路径以默认文件夹为起点，~ 表示用户主目录。`

const readFileToolDesc = `读取文本文件，结果以 cat -n 格式带行号返回，行号从 1 开始，默认最多读取 2000 行。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 已知需要的范围时用 offset 与 limit 只读取该部分。
- 二进制文件与超过 10 MB 的文件无法读取。`

const readImageToolDesc = `
- PNG、JPEG、GIF、WebP 图片以图片形式返回。`

const globToolDesc = `按 glob 模式查找文件，返回按修改时间从新到旧排列的绝对路径。
- 支持 *、**、?、[abc] 与 {a,b}，如 "**/*.xlsx"、"报价/**/*.docx"。
- path 是搜索起点目录，省略时从默认文件夹开始；模式相对起点目录匹配，绝对路径模式从其固定前缀目录开始匹配。
- 跳过 .git 目录。`

const grepToolDesc = `在文件内容中按正则表达式搜索。
- 正则使用 Go RE2 语法，不支持反向引用和环视；字面的特殊字符需要转义，如 interface\{\}。
- path 是搜索的文件或目录，省略时搜索默认文件夹。
- glob 按路径过滤文件，不含 / 的模式匹配文件名，如 "*.go"、"*.{ts,tsx}"；type 按扩展名过滤，如 "go"、"py"。
- output_mode：files_with_matches 只列出文件（默认），content 列出匹配行并可用 -A、-B、-C 显示上下文，count 列出每个文件的匹配数。
- multiline 为 true 时模式可以跨行匹配，. 匹配换行。
- 跳过 .git 目录、二进制文件和超过 2 MB 的文件。`

const writeFileToolDesc = `以完整内容创建或覆盖文件，缺少的上级文件夹会一并创建。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 覆盖已有文件前先用 read_file 读取，局部修改使用 edit_file。
- 只写入文本；Word、Excel、PPT 等文件用 execute 运行脚本生成。`

const editFileToolDesc = `把文本文件中的一段原文精确替换为新内容。
- 修改前先用 read_file 读取文件；old_string 必须与文件内容完全一致（包括缩进与换行），不含 read_file 输出的行号前缀。
- old_string 在文件中必须唯一，否则补充上下文使其唯一；replace_all 为 true 时替换全部出现处。
- new_string 为空表示删除这段原文。`

const deleteFileToolDesc = `删除一个文件或空文件夹。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 删除无法撤销，只删除用户要求或本次任务生成的临时文件。`

const executeToolDesc = `在这台电脑上执行一条 %s 命令，返回合并后的标准输出与标准错误。
- 工作目录是本会话的默认文件夹。
- 单次命令最长运行 10 分钟，超时即终止；命令结束时由它启动的后台进程也会终止，不要启动需要长期运行的服务。
- 命令无法交互输入，需要确认的命令使用非交互参数（如 -y）。
- 查找与读取文件优先使用 glob、grep、read_file，修改文件优先使用 edit_file、write_file。`

// managedToolchainGuidance 是执行设备提供托管运行环境时补充在命令工具说明后的用法。
const managedToolchainGuidance = `
- 命令中可以直接使用 python、uv、uvx、node、npm、npx，这些已预装，无需检查或安装。
- 运行需要第三方包的 Python 脚本时用 uv run --with 包名 python 脚本.py。
- 需要在当前目录保留依赖时，先 uv venv，再 uv pip install 包名，之后用 uv run python 脚本.py 运行；直接执行 python 读不到这个虚拟环境里的包。
- 不要使用 pip、python -m pip、uv pip install --system 或 --break-system-packages，它们会失败或把包装进电脑上原有的 Python。
- 需要 Node.js 包时用 npm 或 npx。`

// deleteFileArgs 是删除文件工具的参数。
type deleteFileArgs struct {
	FilePath string `json:"file_path" jsonschema:"required" jsonschema_description:"要删除的文件或空文件夹路径"`
}

// workspaceTools 是按有效配置创建的本机工具：文件读写、命令执行与技能加载由中间件注册，其余作为普通工具注册。
type workspaceTools struct {
	middlewares []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
	tools       []tool.BaseTool
	names       []string
}

// newWorkspaceTools 按有效配置中的本机工具创建本机文件与命令工具；模型支持图片输入且本机文件可按图片读取时 read_file 以图片返回图片文件，images 为 false 时改按文本读取；skillHub 非空时声明 fork 的技能交给其提供的子 Agent 执行。
func newWorkspaceTools(ctx context.Context, request RunRequest, images *atomic.Bool, skillHub skill.TypedAgentHub[*schema.AgenticMessage]) (workspaceTools, error) {
	names := make([]string, 0, len(localTools))
	for _, name := range request.Assignment.Tools {
		if IsLocalTool(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return workspaceTools{}, nil
	}
	if request.Workspace == nil {
		return workspaceTools{}, fmt.Errorf("agent run assignment requires local tools %v without local file access", names)
	}
	// 按有效配置启用工具，未列出的工具不注册。
	config := func(name, desc string) *fsmiddleware.ToolConfig {
		return &fsmiddleware.ToolConfig{Name: name, Desc: &desc, Disable: !slices.Contains(names, name)}
	}
	readDesc := readFileToolDesc
	var backend filesystem.Backend = request.Workspace
	_, multimodal := backend.(filesystem.MultiModalReader)
	multimodal = multimodal && slices.Contains(request.Assignment.Model.InputModalities, domain.AIModelInputModalityImage)
	if multimodal {
		readDesc += readImageToolDesc
		backend = &imageGatedBackend{Backend: backend, images: images}
	}
	middlewareConfig := &fsmiddleware.MiddlewareConfig{
		Backend:             backend,
		UseMultiModalRead:   multimodal,
		LsToolConfig:        config("ls", lsToolDesc),
		ReadFileToolConfig:  config("read_file", readDesc),
		GlobToolConfig:      config("glob", globToolDesc),
		GrepToolConfig:      config("grep", grepToolDesc),
		WriteFileToolConfig: config("write_file", writeFileToolDesc),
		EditFileToolConfig:  config("edit_file", editFileToolDesc),
	}
	if slices.Contains(names, "execute") {
		// 命令语法随执行设备的操作系统说明。
		shell := "bash"
		if runtime.GOOS == "windows" {
			shell = "PowerShell"
		}
		middlewareConfig.Shell = request.Workspace
		desc := fmt.Sprintf(executeToolDesc, shell)
		if request.ManagedToolchain {
			desc += managedToolchainGuidance
		}
		middlewareConfig.ExecuteToolConfig = &fsmiddleware.ExecuteToolConfig{ToolConfig: *config("execute", desc)}
	}
	middleware, err := fsmiddleware.NewTyped[*schema.AgenticMessage](ctx, middlewareConfig)
	if err != nil {
		return workspaceTools{}, fmt.Errorf("create workspace tools middleware: %w", err)
	}
	result := workspaceTools{middlewares: []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]{middleware}, names: names}
	if slices.Contains(names, "delete_file") {
		deleteTool, err := utils.InferTool("delete_file", deleteFileToolDesc, func(ctx context.Context, input deleteFileArgs) (string, error) {
			if err := request.Workspace.Delete(ctx, input.FilePath); err != nil {
				return "", err
			}
			return "已删除：" + input.FilePath, nil
		})
		if err != nil {
			return workspaceTools{}, fmt.Errorf("create delete file tool: %w", err)
		}
		result.tools = append(result.tools, deleteTool)
	}
	mcpTools, err := newLocalMCPTools(ctx, request)
	if err != nil {
		return workspaceTools{}, err
	}
	result.tools = append(result.tools, mcpTools...)
	skillMiddleware, skillTools, err := newSkillTools(ctx, request, skillHub)
	if err != nil {
		return workspaceTools{}, err
	}
	if skillMiddleware != nil {
		result.middlewares = append(result.middlewares, skillMiddleware)
		result.tools = append(result.tools, skillTools...)
	}
	return result, nil
}

// imageGatedBackend 在图片读取被关闭后把全部文件按文本读取，图片文件因此按二进制文件拒绝读取。
type imageGatedBackend struct {
	filesystem.Backend
	images *atomic.Bool
}

// MultiModalRead 图片读取开启时按图片读取，关闭后按文本读取。
func (b *imageGatedBackend) MultiModalRead(ctx context.Context, req *filesystem.MultiModalReadRequest) (*filesystem.MultiFileContent, error) {
	if b.images.Load() {
		return b.Backend.(filesystem.MultiModalReader).MultiModalRead(ctx, req)
	}
	content, err := b.Backend.Read(ctx, &req.ReadRequest)
	if err != nil {
		return nil, err
	}
	return &filesystem.MultiFileContent{FileContent: content}, nil
}
