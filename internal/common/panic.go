package common

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// PanicError 是由 recover 得到的 panic 转成的错误，保留 panic 发生时的调用栈。
type PanicError struct {
	Value any
	stack []byte
	pcs   []uintptr
}

// NewPanicError 在 recover 所在的延迟函数中调用，记录 panic 值与当时的调用栈。
func NewPanicError(value any) *PanicError {
	pcs := make([]uintptr, 64)
	return &PanicError{Value: value, stack: debug.Stack(), pcs: pcs[:runtime.Callers(2, pcs)]}
}

// Error 返回 panic 值和调用栈文本。
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v\n%s", e.Value, e.stack)
}

// Unwrap 在 panic 值本身是错误时返回该错误。
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// StackTrace 返回 panic 发生时的调用栈程序计数器，由近及远排列。
func (e *PanicError) StackTrace() []uintptr {
	return e.pcs
}
