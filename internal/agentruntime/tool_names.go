package agentruntime

import (
	"github.com/mozillazg/go-pinyin"
	"github.com/runforyou-ai/einorun/toolname"
)

// toolNamePinyinArgs 输出汉字不带声调的读音。
var toolNamePinyinArgs = pinyin.NewArgs()

// toolNamer 生成外部工具的模型可见名称，汉字转写为首个无声调读音。
var toolNamer = toolname.Namer{Transliterate: func(r rune) string {
	if readings := pinyin.SinglePinyin(r, toolNamePinyinArgs); len(readings) > 0 {
		return readings[0]
	}
	return ""
}}

// MCPToolName 生成本机 MCP 工具的模型可见名称：mcp__<服务名>__<工具名>_<摘要>，摘要取自服务标识与原工具名。
func MCPToolName(serverName, toolName string) string {
	return toolNamer.Name("mcp", "local:"+serverName, serverName, toolName)
}

// businessToolName 生成业务系统工具的模型可见名称：biz__<业务系统名>__<工具名>_<摘要>，摘要取自业务系统编号与原工具名。
func businessToolName(systemID, systemName, toolName string) string {
	return toolNamer.Name("biz", "business_system:"+systemID, systemName, toolName)
}
