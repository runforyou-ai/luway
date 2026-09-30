package agentruntime

import "slices"

// localTools 是本机工具目录，按注册顺序排列。
var localTools = []string{"ls", "read_file", "glob", "grep", "write_file", "edit_file", "delete_file", "execute", addLocalMCPToolName, removeLocalMCPToolName, skillToolName, installSkillToolName, removeSkillToolName}

// LocalTools 返回本机工具目录，设备执行的运行按此注册本机工具。
func LocalTools() []string {
	return slices.Clone(localTools)
}

// IsLocalTool 判断工具名称是否属于本机工具目录。
func IsLocalTool(name string) bool {
	return slices.Contains(localTools, name)
}
