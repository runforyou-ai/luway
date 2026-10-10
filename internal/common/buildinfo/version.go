package buildinfo

import (
	"strings"

	"golang.org/x/mod/semver"
)

// CompareVersions 按语义化版本比较两个程序版本，a 较低、相同、较高时分别返回 -1、0、1；dev 等无法解析的版本低于任何发布版本，彼此相同。
func CompareVersions(a, b string) int {
	return semver.Compare(semanticVersion(a), semanticVersion(b))
}

// semanticVersion 为缺少 v 前缀的版本号补上前缀。
func semanticVersion(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
