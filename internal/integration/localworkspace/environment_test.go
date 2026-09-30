package localworkspace

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestEnvironmentApply 验证叠加设置前置 PATH、覆盖同名变量并保留其他变量。
func TestEnvironmentApply(t *testing.T) {
	base := []string{"HOME=/home/user", "PATH=/usr/bin", "NPM_CONFIG_PREFIX=/usr/local"}
	result := Environment{
		PathPrefix: []string{"/tools/uv", "/tools/bin"},
		Variables:  []string{"NPM_CONFIG_PREFIX=/tools/npm"},
	}.apply(base)
	separator := string(os.PathListSeparator)
	expected := []string{"HOME=/home/user", "NPM_CONFIG_PREFIX=/tools/npm", "PATH=" + strings.Join([]string{"/tools/uv", "/tools/bin", "/usr/bin"}, separator)}
	if !slices.Equal(result, expected) {
		t.Fatalf("环境变量不符合预期: %v", result)
	}
	if got := (Environment{}).apply(base); !slices.Equal(got, base) {
		t.Fatalf("零值不应改动环境变量: %v", got)
	}
}
