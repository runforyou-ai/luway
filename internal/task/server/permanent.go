//go:build server

package server

import (
	"context"
	"encoding/json"
	"errors"
)

// Handler 执行已经序列化的 Action 输入。
type Handler func(context.Context, json.RawMessage) error

// PermanentError 表示重试无法修复的 Action 错误。
type PermanentError struct {
	err error
}

// Error 返回 Action 错误。
func (e *PermanentError) Error() string { return e.err.Error() }

// Unwrap 返回原始错误。
func (e *PermanentError) Unwrap() error { return e.err }

// Permanent 将 Action 错误标记为永久失败。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{err: err}
}

// IsPermanent 判断 Action 错误是否为永久失败。
func IsPermanent(err error) bool {
	var target *PermanentError
	return errors.As(err, &target)
}
