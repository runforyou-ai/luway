package agentruntime

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/runforyou-ai/cervi/internal/integration/knowledgeretrieval"
)

// KnowledgeToolName 是知识检索工具名，也是依据来源的登记名。
const KnowledgeToolName = "search_knowledge"

type knowledgeSearchInput struct {
	Queries []string                   `json:"queries,omitempty" jsonschema_description:"用于检索企业知识的查询列表；与 cursor 二选一"`
	Cursor  *knowledgeretrieval.Cursor `json:"cursor,omitempty" jsonschema_description:"检索或阅读结果返回的游标；传入后读取该片段周边内容"`
	Before  int                        `json:"before,omitempty" jsonschema_description:"游标片段之前读取的分段数量"`
	After   int                        `json:"after,omitempty" jsonschema_description:"游标片段之后读取的分段数量"`
}

// newKnowledgeSearchTool 使用本次运行的知识库范围创建检索 Tool。
func newKnowledgeSearchTool(search KnowledgeSearch) (tool.InvokableTool, error) {
	return toolutils.InferTool(
		KnowledgeToolName,
		"检索已配置知识库中的相关资料。传 queries 执行多关键词检索；传返回结果中的 cursor 以及 before、after 读取命中片段周边内容。结果中的 context 是该片段所属的标题路径和表格表头。",
		func(ctx context.Context, input knowledgeSearchInput) (knowledgeretrieval.Result, error) {
			return search(ctx, knowledgeretrieval.Request{
				Queries: input.Queries, Cursor: input.Cursor, Before: input.Before, After: input.After,
			})
		},
	)
}
