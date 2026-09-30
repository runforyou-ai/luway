//go:build !android

package appservice

// Error 返回错误种类或会话状态，以及用户文案。
func (e *Error) Error() string {
	return e.displayMessage()
}
