package agentruntime

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/runforyou-ai/luway/internal/agentcontract"
)

// CustomerHistoryToolName 是服务历史查询工具的名称。
const CustomerHistoryToolName = "search_customer_history"

// customerHistoryInput 是服务历史查询工具的参数。
type customerHistoryInput struct {
	Query string `json:"query,omitempty" jsonschema_description:"可选关键词；留空列出最近的历史消息，填写时按空格分隔多个词，任一命中即返回"`
}

// newCustomerHistoryTool 创建查询同一发起人以往服务沟通记录的工具，search 为空时调用返回不可用。
func newCustomerHistoryTool(search CustomerHistorySearch) (tool.InvokableTool, error) {
	return toolutils.InferTool(
		CustomerHistoryToolName,
		"查询同一发起人以往已结束的服务沟通。默认跨周期合计列出最近 30 条历史消息，单条正文最多 500 字符，按周期分组、周期内按时间正序；可选 query 筛选关键词并带出命中消息前后各 2 条。sender 为 customer 表示发起人、agent 表示 AI 员工、member 表示处理同事，attachment 是附件文件名。",
		func(ctx context.Context, input customerHistoryInput) (agentcontract.CustomerHistoryResult, error) {
			if search == nil {
				return agentcontract.CustomerHistoryResult{}, errToolUnavailable
			}
			return search(ctx, input.Query)
		},
	)
}
