//go:build android

package appservice

import "encoding/base64"

const androidErrorMarker = "\n__APP_API_ERROR_V1__:"

// Error 在 Android 错误文本中附加 Base64 编码的结构化载荷。
func (e *Error) Error() string {
	message := e.displayMessage()
	payload := MarshalError(e)
	if payload == nil {
		return message
	}
	return message + androidErrorMarker + base64.StdEncoding.EncodeToString(payload)
}
