//go:build server

package device

import "errors"

// ErrNotFound 表示当前用户名下不存在指定的未撤销设备。
var ErrNotFound = errors.New("device not found")
