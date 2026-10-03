// Package modelgateway 定义设备经服务端模型网关调用对话模型的请求与流式响应编码，并提供设备侧的模型组件。
package modelgateway

import (
	"encoding/gob"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	_ "github.com/runforyou-ai/luway/internal/integration/modelprovider"
)

// ContentType 是网关请求体与响应体的媒体类型，两者均为 gob 编码。
const ContentType = "application/x-gob"

// MaxRequestBytes 是单次模型请求体的上限，覆盖随消息直传的附件。
const MaxRequestBytes = 64 << 20

// Request 定义一次模型请求：创建模型组件的参数、上下文消息与通用调用选项。
type Request struct {
	Model    agentruntime.ModelOptions
	Stream   bool
	Messages []*schema.AgenticMessage
	Options  Options
}

// Options 定义可跨进程传递的通用模型调用选项。
type Options struct {
	Temperature       *float32
	TopP              *float32
	MaxTokens         *int
	Stop              []string
	Tools             []Tool
	DeferredTools     []Tool
	ToolSearchTool    *Tool
	ToolChoice        *schema.ToolChoice
	AllowedToolNames  []string
	AgenticToolChoice *schema.AgenticToolChoice
}

// Tool 定义可跨进程传递的工具说明，参数以 JSON Schema 文本表示，为空表示工具没有参数。
type Tool struct {
	Name       string
	Desc       string
	Extra      map[string]any
	Parameters []byte
}

// Frame 是响应流中的一帧：模型输出的一个分片，或结束调用的错误说明。
type Frame struct {
	Message *schema.AgenticMessage
	Error   string
}

// EncodeOptions 把调用方传入的模型选项转换为可跨进程传递的选项。
func EncodeOptions(opts []model.Option) (Options, error) {
	common := model.GetCommonOptions(nil, opts...)
	encoded := Options{
		Temperature: common.Temperature, TopP: common.TopP, MaxTokens: common.MaxTokens, Stop: common.Stop,
		ToolChoice: common.ToolChoice, AllowedToolNames: common.AllowedToolNames, AgenticToolChoice: common.AgenticToolChoice,
	}
	var err error
	if encoded.Tools, err = encodeTools(common.Tools); err != nil {
		return Options{}, err
	}
	if encoded.DeferredTools, err = encodeTools(common.DeferredTools); err != nil {
		return Options{}, err
	}
	if common.ToolSearchTool != nil {
		tool, err := encodeTool(common.ToolSearchTool)
		if err != nil {
			return Options{}, err
		}
		encoded.ToolSearchTool = &tool
	}
	return encoded, nil
}

// ModelOptions 把跨进程传递的选项还原为模型调用选项。
func (o Options) ModelOptions() ([]model.Option, error) {
	opts := make([]model.Option, 0, 10)
	if o.Temperature != nil {
		opts = append(opts, model.WithTemperature(*o.Temperature))
	}
	if o.TopP != nil {
		opts = append(opts, model.WithTopP(*o.TopP))
	}
	if o.MaxTokens != nil {
		opts = append(opts, model.WithMaxTokens(*o.MaxTokens))
	}
	if len(o.Stop) > 0 {
		opts = append(opts, model.WithStop(o.Stop))
	}
	if len(o.Tools) > 0 {
		tools, err := decodeTools(o.Tools)
		if err != nil {
			return nil, err
		}
		opts = append(opts, model.WithTools(tools))
	}
	if len(o.DeferredTools) > 0 {
		tools, err := decodeTools(o.DeferredTools)
		if err != nil {
			return nil, err
		}
		opts = append(opts, model.WithDeferredTools(tools))
	}
	if o.ToolSearchTool != nil {
		tool, err := decodeTool(*o.ToolSearchTool)
		if err != nil {
			return nil, err
		}
		opts = append(opts, model.WithToolSearchTool(tool))
	}
	if o.ToolChoice != nil {
		opts = append(opts, model.WithToolChoice(*o.ToolChoice, o.AllowedToolNames...))
	}
	if o.AgenticToolChoice != nil {
		opts = append(opts, model.WithAgenticToolChoice(o.AgenticToolChoice))
	}
	return opts, nil
}

// encodeTools 转换一组工具说明。
func encodeTools(tools []*schema.ToolInfo) ([]Tool, error) {
	encoded := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		item, err := encodeTool(tool)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, item)
	}
	return encoded, nil
}

// encodeTool 把工具说明的参数转换为 JSON Schema 文本。
func encodeTool(tool *schema.ToolInfo) (Tool, error) {
	encoded := Tool{Name: tool.Name, Desc: tool.Desc, Extra: tool.Extra}
	if tool.ParamsOneOf == nil {
		return encoded, nil
	}
	parameters, err := tool.ToJSONSchema()
	if err != nil {
		return Tool{}, fmt.Errorf("convert tool %q parameters: %w", tool.Name, err)
	}
	if encoded.Parameters, err = json.Marshal(parameters); err != nil {
		return Tool{}, fmt.Errorf("encode tool %q parameters: %w", tool.Name, err)
	}
	return encoded, nil
}

// decodeTools 还原一组工具说明。
func decodeTools(tools []Tool) ([]*schema.ToolInfo, error) {
	decoded := make([]*schema.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		item, err := decodeTool(tool)
		if err != nil {
			return nil, err
		}
		decoded = append(decoded, item)
	}
	return decoded, nil
}

// decodeTool 由 JSON Schema 文本还原工具说明。
func decodeTool(tool Tool) (*schema.ToolInfo, error) {
	decoded := &schema.ToolInfo{Name: tool.Name, Desc: tool.Desc, Extra: tool.Extra}
	if len(tool.Parameters) == 0 {
		return decoded, nil
	}
	parameters := &jsonschema.Schema{}
	if err := json.Unmarshal(tool.Parameters, parameters); err != nil {
		return nil, fmt.Errorf("decode tool %q parameters: %w", tool.Name, err)
	}
	decoded.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(parameters)
	return decoded, nil
}

// 网关两端都依赖 modelprovider，加载各供应商组件注册的消息扩展类型，按相同类型编解码；另注册附加信息中的常见取值。
func init() {
	gob.Register(map[string]any{})
	gob.Register([]any{})
}
