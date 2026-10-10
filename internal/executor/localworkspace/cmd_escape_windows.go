package localworkspace

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// cmdMetaChars 是 cmd.exe 解析命令行时有特殊含义的字符。
var cmdMetaChars = regexp.MustCompile("([()\\][%!^\"`<>&|;, *?])")

// quotedBackslashes 匹配引号前的连续反斜杠。
var quotedBackslashes = regexp.MustCompile(`(\\*)"`)

// trailingBackslashes 匹配结尾的连续反斜杠。
var trailingBackslashes = regexp.MustCompile(`(\\*)$`)

// reparsesArguments 判断脚本是否在注释以外的行中使用未加引号的 %*，这样的 %* 展开后会被 cmd.exe 再解析一次。
func reparsesArguments(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.ToLower(strings.TrimLeft(line, " \t@"))
		if strings.HasPrefix(trimmed, "::") || trimmed == "rem" || strings.HasPrefix(trimmed, "rem ") {
			continue
		}
		quoted := false
		for index := 0; index < len(line); index++ {
			switch {
			case line[index] == '"':
				quoted = !quoted
			case !quoted && strings.HasPrefix(line[index:], "%*"):
				return true
			}
		}
	}
	return false
}

// cmdUnsafeChars 是 cmd.exe 命令行中无法可靠转义的字符：换行、回车与 NUL 会截断命令行，% 在转义前就会展开环境变量。
const cmdUnsafeChars = "\r\n\x00%"

// cmdScriptLine 返回经 cmd.exe /d /s /c 执行脚本的命令行：脚本路径用 ^ 转义 cmd.exe 的特殊字符；参数按 C 运行库规则加引号后，再用 ^ 转义特殊字符，
// 脚本以未加引号的 %* 把参数交给 cmd.exe 再解析一次（如 npm 生成的 .cmd）时参数再转义一次。路径或参数含无法转义的字符时返回错误。
func cmdScriptLine(interpreter, script string, args []string) (string, error) {
	for _, value := range append([]string{script}, args...) {
		if strings.ContainsAny(value, cmdUnsafeChars) {
			return "", fmt.Errorf("批处理脚本的路径或参数不能包含换行、回车、NUL 或 %%：%q", value)
		}
	}
	content, err := os.ReadFile(script)
	if err != nil {
		return "", fmt.Errorf("无法读取批处理脚本：%w", err)
	}
	doubleEscape := reparsesArguments(string(content))
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, cmdMetaChars.ReplaceAllString(script, "^$1"))
	for _, arg := range args {
		arg = quotedBackslashes.ReplaceAllString(arg, `$1$1\"`)
		arg = trailingBackslashes.ReplaceAllString(arg, "$1$1")
		arg = cmdMetaChars.ReplaceAllString(`"`+arg+`"`, "^$1")
		if doubleEscape {
			arg = cmdMetaChars.ReplaceAllString(arg, "^$1")
		}
		parts = append(parts, arg)
	}
	return `"` + interpreter + `" /d /s /c "` + strings.Join(parts, " ") + `"`, nil
}
