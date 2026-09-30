package common

// FieldCode 标识一条字段校验结果。
type FieldCode string

// FieldError 记录字段校验失败。
type FieldError struct {
	Fields map[string]FieldCode
}

// Error 返回字段校验失败说明。
func (e *FieldError) Error() string {
	return "validation failed"
}
