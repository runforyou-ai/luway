// Package timezone 提供 IANA 时区校验能力。
package timezone

import (
	"time"
	_ "time/tzdata"
)

// Valid 判断名称是否为 Go 时区数据库支持的 IANA 时区。
func Valid(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
