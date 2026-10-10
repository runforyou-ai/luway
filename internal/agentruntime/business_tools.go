package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/set"
)

// newBusinessTools 按业务系统顺序把挂载的工具注册为模型可调用工具，参数定义无法解析或名称与已注册工具重复的工具跳过。
func newBusinessTools(ctx context.Context, runID string, systems []agentcontract.BusinessSystem, registered set.Set[string]) []*businessTool {
	tools := make([]*businessTool, 0)
	for index := range systems {
		system := &systems[index]
		for _, mount := range system.Tools {
			info, err := businessToolInfo(system, mount)
			if err != nil {
				slog.WarnContext(ctx, "业务系统工具参数定义无法解析，跳过该工具",
					"agent_run_id", runID, "business_system_id", system.ID, "tool_name", mount.Name, "error", err)
				continue
			}
			if !registered.Add(info.Name) {
				slog.WarnContext(ctx, "业务系统工具名称与已注册工具重复，跳过该工具",
					"agent_run_id", runID, "business_system_id", system.ID, "tool_name", mount.Name)
				continue
			}
			tools = append(tools, &businessTool{system: system, mount: mount, info: info})
		}
	}
	return tools
}

// businessToolInfo 把挂载的工具转换为模型可见的工具定义，描述开头注明业务系统名称，需要人工介入的工具在末尾注明；绑定的参数从参数定义中移除。
func businessToolInfo(system *agentcontract.BusinessSystem, mount agentcontract.BusinessToolMount) (*schema.ToolInfo, error) {
	parameters := &jsonschema.Schema{Type: "object"}
	if len(mount.InputSchema) > 0 {
		if err := json.Unmarshal(mount.InputSchema, parameters); err != nil {
			return nil, err
		}
	}
	for name := range mount.Bound {
		if parameters.Properties != nil {
			parameters.Properties.Delete(name)
		}
	}
	parameters.Required = slices.DeleteFunc(slices.Clone(parameters.Required), func(name string) bool {
		_, bound := mount.Bound[name]
		return bound
	})
	description := strings.TrimSpace(fmt.Sprintf("［业务系统 · %s］%s", system.Name, mount.Description))
	if note, ok := interventionNotes[mount.Intervention]; ok {
		description += note
	}
	return &schema.ToolInfo{
		Name:        businessToolName(system.ID, system.Name, mount.Name),
		Desc:        description,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameters),
	}, nil
}

// interventionNotes 是需要人工介入的工具在描述末尾注明的执行方式。
var interventionNotes = map[domain.ToolIntervention]string{
	domain.ToolInterventionConfirmation: "\n（调用后先提交给用户确认，确认后才会执行，结果稍后送达。）",
	domain.ToolInterventionApproval:     "\n（调用后先提交给负责人审批，批准后才会执行，结果稍后送达。）",
}

// businessTool 把业务系统工具暴露为 Eino 可调用工具，调用时填入绑定参数并经业务系统的调用会话发出。
type businessTool struct {
	system *agentcontract.BusinessSystem
	mount  agentcontract.BusinessToolMount
	info   *schema.ToolInfo
}

// Info 返回模型可见的名称、描述和参数定义。
func (t *businessTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 填入绑定参数后调用业务系统工具并返回文本结果。
func (t *businessTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	arguments := json.RawMessage(argumentsInJSON)
	if !json.Valid(arguments) {
		return "", errors.New("参数不是合法 JSON，请重新提交。")
	}
	arguments, err := agentcontract.BindArguments(arguments, t.mount.Bound)
	if err != nil {
		return "", err
	}
	result, err := t.system.Caller.Call(ctx, t.mount.Name, arguments)
	if err != nil {
		return "", fmt.Errorf("call tool %q of business system %q: %w", t.mount.Name, t.system.Name, err)
	}
	return result, nil
}

// closeBusinessCallers 关闭本次运行全部业务系统的调用会话。
func closeBusinessCallers(systems []agentcontract.BusinessSystem) {
	for _, system := range systems {
		if err := system.Caller.Close(); err != nil {
			slog.WarnContext(context.Background(), "关闭业务系统调用会话失败", "business_system_id", system.ID, "error", err)
		}
	}
}
