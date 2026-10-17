package common

import "errors"

// 分页的默认页码、默认页大小与最大页大小。
const (
	defaultPage     = 1
	defaultPageSize = 50
	maximumPageSize = 100
)

// ErrPageSizeInvalid 表示每页数量超出上限。
var ErrPageSizeInvalid = errors.New("page size invalid")

// NormalizePagination 补齐分页默认值并判断每页数量是否有效。
func NormalizePagination(page, pageSize int) (int, int, bool) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize, pageSize <= maximumPageSize
}
