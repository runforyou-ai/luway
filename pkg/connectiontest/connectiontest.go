// Package connectiontest 定义外部连接访问的错误分类与探测语义。
package connectiontest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Stage 标识外部连接失败阶段。
type Stage string

const (
	StageConnect      Stage = "connect"
	StageAuthenticate Stage = "authenticate"
	StageAuthorize    Stage = "authorize"
	StageCapability   Stage = "capability"
)

// FailureKind 标识与具体外部服务无关的失败类别。
type FailureKind string

const (
	FailureInvalidConfig FailureKind = "invalid_config"
	FailureUnauthorized  FailureKind = "unauthorized"
	FailureForbidden     FailureKind = "forbidden"
	FailureNotFound      FailureKind = "not_found"
	FailureRateLimited   FailureKind = "rate_limited"
	FailureTimeout       FailureKind = "timeout"
	FailureNetwork       FailureKind = "network"
	FailureTLS           FailureKind = "tls"
	FailureProtocol      FailureKind = "protocol"
	FailureUnavailable   FailureKind = "unavailable"
)

// Target 定义写入探测日志的对象类别、适配器和执行位置。
type Target struct {
	Category string
	Adapter  string
	Location string
}

// Probe 定义具体外部连接适配器需要实现的最小契约。
type Probe interface {
	Run(context.Context) error
}

// ProbeFunc 把函数适配为连接探测器。
type ProbeFunc func(context.Context) error

// Run 执行连接探测函数。
func (f ProbeFunc) Run(ctx context.Context) error {
	return f(ctx)
}

// Error 描述已经分类的外部连接错误。
type Error struct {
	Stage Stage
	Kind  FailureKind
	err   error
}

// Error 返回外部连接错误描述。
func (e *Error) Error() string {
	if e.err == nil {
		return fmt.Sprintf("external connection failed at %s: %s", e.Stage, e.Kind)
	}
	return fmt.Sprintf("external connection failed at %s: %s: %v", e.Stage, e.Kind, e.err)
}

// Unwrap 返回原始错误。
func (e *Error) Unwrap() error {
	return e.err
}

// NewError 创建已经分类的外部连接错误。
func NewError(stage Stage, kind FailureKind, err error) *Error {
	return &Error{Stage: stage, Kind: kind, err: err}
}

// Details 返回外部连接错误的失败阶段和类别。
func Details(err error) (Stage, FailureKind, bool) {
	var connectionError *Error
	if !errors.As(err, &connectionError) {
		return "", "", false
	}
	return connectionError.Stage, connectionError.Kind, true
}

// ClassifyTransportError 规范化 HTTP 客户端或其他网络传输错误。
func ClassifyTransportError(stage Stage, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewError(stage, FailureTimeout, err)
	}
	var certificateInvalid x509.CertificateInvalidError
	var hostnameInvalid x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	var recordHeader tls.RecordHeaderError
	if errors.As(err, &certificateInvalid) || errors.As(err, &hostnameInvalid) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &recordHeader) {
		return NewError(stage, FailureTLS, err)
	}
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Timeout() {
		return NewError(stage, FailureTimeout, err)
	}
	if networkError, ok := errors.AsType[net.Error](err); ok {
		if networkError.Timeout() {
			return NewError(stage, FailureTimeout, err)
		}
		return NewError(stage, FailureNetwork, err)
	}
	return NewError(stage, FailureUnavailable, err)
}

// HTTPStatusError 按外部服务响应状态规范化探测错误。
func HTTPStatusError(status int, details ...string) *Error {
	cause := fmt.Errorf("unexpected HTTP status %d", status)
	if len(details) > 0 && details[0] != "" {
		cause = fmt.Errorf("unexpected HTTP status %d: %s", status, details[0])
	}
	switch status {
	case http.StatusUnauthorized:
		return NewError(StageAuthenticate, FailureUnauthorized, cause)
	case http.StatusForbidden:
		return NewError(StageAuthorize, FailureForbidden, cause)
	case http.StatusNotFound:
		return NewError(StageCapability, FailureNotFound, cause)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return NewError(StageConnect, FailureTimeout, cause)
	case http.StatusTooManyRequests:
		return NewError(StageCapability, FailureRateLimited, cause)
	}
	if status >= http.StatusInternalServerError {
		return NewError(StageConnect, FailureUnavailable, cause)
	}
	return NewError(StageCapability, FailureProtocol, cause)
}

// Runner 统一处理连接探测的超时和可观测信息。
type Runner struct {
	timeout time.Duration
}

// NewRunner 创建具有统一超时的连接探测执行器。
func NewRunner(timeout time.Duration) *Runner {
	return &Runner{timeout: timeout}
}

// Run 执行单次连接探测。
func (r *Runner) Run(ctx context.Context, target Target, probe Probe) error {
	startedAt := time.Now()
	testCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	err := probe.Run(testCtx)
	duration := time.Since(startedAt)
	if err == nil {
		slog.Info("外部连接测试成功", connectionLogAttributes(target, duration)...)
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, _, classified := Details(err); !classified {
		if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			err = NewError(StageConnect, FailureTimeout, context.DeadlineExceeded)
		} else {
			err = ClassifyTransportError(StageConnect, err)
		}
	}
	stage, kind, _ := Details(err)
	attributes := append(connectionLogAttributes(target, duration), "stage", stage, "kind", kind)
	slog.Warn("外部连接测试失败", attributes...)
	return err
}

// connectionLogAttributes 返回不含地址和凭据的连接探测日志字段。
func connectionLogAttributes(target Target, duration time.Duration) []any {
	return []any{
		"category", target.Category,
		"adapter", target.Adapter,
		"location", target.Location,
		"duration_ms", duration.Milliseconds(),
	}
}
