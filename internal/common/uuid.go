package common

import (
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
)

// NormalizeUUIDs 规范化记录标识列表、保持顺序并去重。
func NormalizeUUIDs(values []string) ([]string, bool) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, valid := str.NormalizeUUID(value)
		if !valid {
			return nil, false
		}
		result = append(result, normalized)
	}
	return arr.Unique(result), true
}
