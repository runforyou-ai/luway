package localworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript 在临时目录写入批处理脚本并返回路径。
func writeScript(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCmdScriptLineEscapesArguments 验证参数加引号并转义 cmd.exe 特殊字符，只有经 %* 转发参数的脚本再转义一次。
func TestCmdScriptLineEscapesArguments(t *testing.T) {
	plain := writeScript(t, "tool.cmd", "@echo off\r\necho %~1\r\n")
	line, err := cmdScriptLine("cmd.exe", plain, []string{"-y", `C:\R&D`, `say "hi"`, `dir\`})
	if err != nil {
		t.Fatal(err)
	}
	escapedPlain := strings.ReplaceAll(plain, " ", "^ ")
	if expected := `"cmd.exe" /d /s /c "` + escapedPlain + ` ^"-y^" ^"C:\R^&D^" ^"say^ \^"hi\^"^" ^"dir\\^""`; line != expected {
		t.Fatalf("普通脚本的命令行不符合预期:\n%s\n%s", line, expected)
	}
	shim := writeScript(t, "npx.cmd", "@\"%_prog%\" \"%dp0%\\node_modules\\npm\\bin\\npx-cli.js\" %*\r\n")
	line, err = cmdScriptLine("cmd.exe", shim, []string{`C:\R&D`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(line, ` ^^^"C:\R^^^&D^^^""`) {
		t.Fatalf("转发参数的脚本应再转义一次: %s", line)
	}
}

// TestCmdScriptLineRejectsUnsafeCharacters 验证路径或参数含换行、回车、NUL 或 % 时拒绝生成命令行。
func TestCmdScriptLineRejectsUnsafeCharacters(t *testing.T) {
	script := writeScript(t, "tool.cmd", "@echo off\r\n")
	for _, arg := range []string{"a\nb", "a\rb", "a\x00b", "%PATH%", "a%20b"} {
		if _, err := cmdScriptLine("cmd.exe", script, []string{arg}); err == nil {
			t.Fatalf("应拒绝参数 %q", arg)
		}
	}
}

// TestReparsesArguments 验证只有注释以外、未加引号的 %* 视为再解析参数。
func TestReparsesArguments(t *testing.T) {
	for content, expected := range map[string]bool{
		"@\"%_prog%\" \"%dp0%\\npx-cli.js\" %*\r\n": true,
		"@echo %*\r\n":                                     true,
		"node server.js \"%*\"\r\n":                        false,
		":: forwards %*\r\nnode server.js %~1\r\n":         false,
		"REM uses %*\r\nnode server.js\r\n":                false,
		"node server.js %~1\r\n":                           false,
		"@rem usage: tool.cmd %*\r\n@node tool.js %~1\r\n": false,
	} {
		if reparsesArguments(content) != expected {
			t.Fatalf("%q 的判断应为 %v", content, expected)
		}
	}
}
