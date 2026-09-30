package agentruntime

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

// CustomerHistoryToolName 是客户历史检索工具的名称。
const CustomerHistoryToolName = "search_customer_history"

type customerHistoryInput struct {
	Query string `json:"query" jsonschema_description:"检索词，多个检索词用空格分隔，任一命中即返回，如订单号、商品名或问题关键词"`
}

// newCustomerHistoryTool 创建检索同一客户以往客服沟通记录的工具。
func newCustomerHistoryTool(search CustomerHistorySearch) (tool.InvokableTool, error) {
	return toolutils.InferTool(
		CustomerHistoryToolName,
		"检索同一客户以往已结束的客服沟通，返回命中的咨询小结与命中消息前后的沟通记录，sender 为 customer 表示客户、agent 表示 AI 员工、member 表示真人客服，attachment 是消息附带的文件名。",
		func(ctx context.Context, input customerHistoryInput) (CustomerHistoryResult, error) {
			return search(ctx, input.Query)
		},
	)
}
