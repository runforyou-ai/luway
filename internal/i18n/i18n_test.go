package i18n

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// TestKeyConstantsMatchLocaleFiles 验证 Key 常量与各语言词条键集合一致。
func TestKeyConstantsMatchLocaleFiles(t *testing.T) {
	constants := collectKeyConstants(t)
	if len(constants) == 0 {
		t.Fatal("未在包内源文件中找到任何 Key 常量")
	}

	for _, path := range []string{"locales/en-US.json", "locales/zh-CN.json"} {
		localeKeys := readLocaleKeys(t, path)

		var missing []string
		for key := range constants {
			if _, ok := localeKeys[key]; !ok {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s 缺少 Key 常量对应的词条: %v", path, missing)
		}

		var extra []string
		for key := range localeKeys {
			if _, ok := constants[key]; !ok {
				extra = append(extra, key)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			t.Errorf("%s 存在没有 Key 常量对应的多余词条: %v", path, extra)
		}
	}
}

// TestLocalizeMatchesRequestedLanguage 验证本地化器匹配请求语言并回退到英文。
func TestLocalizeMatchesRequestedLanguage(t *testing.T) {
	tests := []struct {
		acceptLanguage string
		wantMessage    string
		wantLanguage   string
	}{
		{acceptLanguage: "zh-CN", wantMessage: "请先登录。", wantLanguage: "zh-CN"},
		{acceptLanguage: "en-US", wantMessage: "Please log in first.", wantLanguage: "en-US"},
		{acceptLanguage: "fr-FR", wantMessage: "Please log in first.", wantLanguage: "en-US"},
	}

	for _, test := range tests {
		message, matchedLanguage := Localize(test.acceptLanguage, ErrorAuthenticationRequired)
		if message != test.wantMessage || matchedLanguage != test.wantLanguage {
			t.Fatalf(
				"Localize(%q) = (%q, %q), want (%q, %q)",
				test.acceptLanguage,
				message,
				matchedLanguage,
				test.wantMessage,
				test.wantLanguage,
			)
		}
	}
}

// collectKeyConstants 解析包目录内所有非测试源文件，收集类型为 Key 的常量的字符串字面量值。
func collectKeyConstants(t *testing.T) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	fileSet := token.NewFileSet()
	keys := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				ident, ok := valueSpec.Type.(*ast.Ident)
				if !ok || ident.Name != "Key" {
					continue
				}
				for _, value := range valueSpec.Values {
					lit, ok := value.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					key, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					keys[key] = struct{}{}
				}
			}
		}
	}
	return keys
}

// readLocaleKeys 读取指定语言文件并返回其中的文案键集合。
func readLocaleKeys(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	content, err := localeFiles.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	messages := make(map[string]json.RawMessage)
	if err := json.Unmarshal(content, &messages); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]struct{}, len(messages))
	for key := range messages {
		keys[key] = struct{}{}
	}
	return keys
}

// TestLocalizeInjectsProductName 验证文案按匹配到的语言插入当前品牌的产品名称。
func TestLocalizeInjectsProductName(t *testing.T) {
	product := brand.Current()
	english, _ := Localize("en-US", AppTrayOpen)
	if english != "Open "+product.Name("en-US") {
		t.Fatalf("英文托盘文案 = %q", english)
	}
	chinese, _ := Localize("zh-CN,zh;q=0.9", AppTrayOpen)
	if chinese != "打开"+product.Name("zh-CN") {
		t.Fatalf("中文托盘文案 = %q", chinese)
	}
	body := LocalizeTemplate("zh-CN", InvitationEmailBody, map[string]any{"Inviter": "张三", "Workspace": "销售部"})
	if !strings.Contains(body, product.Name("zh-CN")+"工作区「销售部」") {
		t.Fatalf("邀请邮件正文 = %q", body)
	}
}
