//go:build server

package contact

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定联系人。
	ErrNotFound = errors.New("contact not found")
)
