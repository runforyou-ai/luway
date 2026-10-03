package agentruntime

import "slices"

// SkillToolName 是加载电脑上技能说明的工具名称。
const SkillToolName = "skill"

// computerTools 是在电脑上执行的内置工具目录，按注册顺序排列。
var computerTools = []string{readFileToolName, writeFileToolName, editFileToolName, executeToolName, skillToolName}

// ComputerTools 返回电脑工具目录，使用电脑的运行按此注册电脑工具。
func ComputerTools() []string {
	return slices.Clone(computerTools)
}

// IsComputerTool 判断工具名称是否属于电脑工具目录。
func IsComputerTool(name string) bool {
	return slices.Contains(computerTools, name)
}
