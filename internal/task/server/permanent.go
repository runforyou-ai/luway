//go:build server

package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/runforyou-ai/jetq"
)

// Handler 执行已经序列化的 Action 输入。
type Handler func(context.Context, json.RawMessage) error

// Permanent 将 Action 错误标记为永久失败，任务不再重试。
func Permanent(err error) error { return jetq.Permanent(err) }

// IsPermanent 判断 Action 错误是否为永久失败。
func IsPermanent(err error) bool { return jetq.IsPermanent(err) }

// RetryAfter 让本次尝试失败并在 delay 后重试，尝试次数照常计入。
func RetryAfter(delay time.Duration, err error) error { return jetq.RetryAfter(delay, err) }
