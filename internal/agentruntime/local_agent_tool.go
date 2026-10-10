package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/luway/internal/domain"
)

// localAgentToolName 是把任务委派给电脑上本机 Agent 的工具名称。
const localAgentToolName = "local_agent"

// localAgentToolDesc 是委派本机 Agent 工具的说明模板，%s 处列出可用的本机 Agent。
const localAgentToolDesc = `把任务交给电脑上的本机 Agent 完成，适合编程、整理文件等需要在电脑上连续操作的工作。可用的本机 Agent：
%s
- request 原样填写用户的原话，不改写、不总结、不补充。
- context 填写本机 Agent 需要的补充信息：此前讨论的结论、用户提到的文件与报错、上一次交给它的结果要点；本机 Agent 看不到本次对话，也看不到你的记忆。
- 同一会话中再次交给同一个本机 Agent 时默认续接，它记得此前各轮；用户要求重新开始或换一件无关的事时 new_session 填 true。
- 本机 Agent 的工作目录是本会话的默认文件夹，其中的 shared/ 是共享文件区的副本，每一轮前后自动同步；需要它处理共享文件时在 context 中写明 shared/ 下的路径。
- 调用后立即返回，本机 Agent 的执行过程与最终回复会直接展示给用户。不要等待结果，也不要复述或总结它的回复，用一句话告诉用户已交给它处理。`

// localAgentArgs 是委派本机 Agent 工具的参数。
type localAgentArgs struct {
	Agent      string `json:"agent"`
	Request    string `json:"request"`
	Context    string `json:"context"`
	NewSession bool   `json:"new_session"`
}

// newLocalAgentTool 创建委派本机 Agent 的工具，agent 参数的取值为电脑上可用的本机 Agent 名称。
func newLocalAgentTool(agents []domain.ComputerLocalAgent) (*computerTool, error) {
	names := make([]any, 0, len(agents))
	lines := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, agent.Name)
		line := "- " + agent.Name
		if description := strings.TrimSpace(agent.Description); description != "" {
			line += "：" + description
		}
		lines = append(lines, line)
	}
	definition, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"agent", "request"},
		"properties": map[string]any{
			"agent":       map[string]any{"type": "string", "enum": names, "description": "交给哪个本机 Agent"},
			"request":     map[string]any{"type": "string", "description": "用户的原话"},
			"context":     map[string]any{"type": "string", "description": "补充上下文"},
			"new_session": map[string]any{"type": "boolean", "description": "是否开启新会话"},
		},
	})
	if err != nil {
		return nil, err
	}
	parameters := &jsonschema.Schema{}
	if err := json.Unmarshal(definition, parameters); err != nil {
		return nil, err
	}
	return &computerTool{
		info: &schema.ToolInfo{
			Name: localAgentToolName, Desc: fmt.Sprintf(localAgentToolDesc, strings.Join(lines, "\n")),
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameters),
		},
		operation: func(arguments string) (domain.ComputerOperation, error) {
			var input localAgentArgs
			if err := json.Unmarshal([]byte(arguments), &input); err != nil {
				return domain.ComputerOperation{}, errors.New("参数不是合法 JSON，请重新提交。")
			}
			if !slices.ContainsFunc(agents, func(agent domain.ComputerLocalAgent) bool { return agent.Name == input.Agent }) {
				return domain.ComputerOperation{}, fmt.Errorf("这台电脑上没有可用的本机 Agent「%s」，agent 只能填写工具说明中列出的名称。", input.Agent)
			}
			if strings.TrimSpace(input.Request) == "" {
				return domain.ComputerOperation{}, errors.New("request 不能为空，请填写用户的原话后重新提交。")
			}
			return domain.ComputerOperation{
				Kind: domain.ComputerOperationLocalAgent, LocalAgent: input.Agent, NewSession: input.NewSession,
				Prompt: LocalAgentPrompt(input.Request, input.Context),
			}, nil
		},
		detached: true,
	}, nil
}

// LocalAgentPrompt 组成交给本机 Agent 的提示：没有补充上下文时为用户原话本身，否则分别标明用户原话与补充上下文。
func LocalAgentPrompt(request, context string) string {
	if strings.TrimSpace(context) == "" {
		return request
	}
	return "## 用户的原话\n\n" + request + "\n\n## 补充上下文\n\n" + strings.TrimSpace(context)
}
