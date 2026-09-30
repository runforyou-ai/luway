//go:build !server

package apiproxy

import (
	"net/url"
	"reflect"
	"strings"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// appservicePackage 限定文件地址补全的遍历范围，遇到 time.Time 等外部类型即停止。
var appservicePackage = reflect.TypeFor[appservice.RequestMeta]().PkgPath()

// localFilePrefix 是服务端返回的本地存储文件相对路径前缀。
const localFilePrefix = domain.LocalFilePublicPath + "/"

// resolveFileURLs 把结果中字段名以 URL 结尾、值为本地存储相对路径的字段补全为当前连接地址下的绝对地址；output 必须是指向结果的指针。
func resolveFileURLs(output any, baseURL *url.URL) {
	resolveFileURLValue(reflect.ValueOf(output), baseURL)
}

// resolveFileURLValue 递归遍历指针、结构体和切片，就地补全本地存储文件地址。
func resolveFileURLValue(value reflect.Value, baseURL *url.URL) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			resolveFileURLValue(value.Elem(), baseURL)
		}
	case reflect.Struct:
		if value.Type().PkgPath() != appservicePackage {
			return
		}
		for index := range value.NumField() {
			field := value.Field(index)
			// 只改写 URL 字段中的本地存储相对路径，绝对地址与其他字符串保持原样。
			if field.Kind() == reflect.String && strings.HasSuffix(value.Type().Field(index).Name, "URL") {
				if path := field.String(); strings.HasPrefix(path, localFilePrefix) && field.CanSet() {
					field.SetString(strings.TrimRight(baseURL.String(), "/") + path)
				}
				continue
			}
			resolveFileURLValue(field, baseURL)
		}
	case reflect.Slice:
		for index := range value.Len() {
			resolveFileURLValue(value.Index(index), baseURL)
		}
	}
}
