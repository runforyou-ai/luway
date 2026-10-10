package appservice

import (
	"reflect"
	"strings"
)

// modulePackagePrefix 限定空值归一化遍历的结构体所属模块，遇到 time.Time 等模块外类型即停止。
var modulePackagePrefix = strings.Split(reflect.TypeFor[RequestMeta]().PkgPath(), "/internal/")[0] + "/"

// NormalizeEmpty 把结果中的 nil 切片与映射就地替换为空值，HTTP 响应与本机能力调用据此把它们输出为空数组与空对象。
func NormalizeEmpty[T any](output *T) {
	normalizeValue(reflect.ValueOf(output))
}

// normalizeValue 递归遍历指针、结构体、切片与映射，就地补齐 nil 切片与映射。
func normalizeValue(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			normalizeValue(value.Elem())
		}
	case reflect.Struct:
		if !strings.HasPrefix(value.Type().PkgPath(), modulePackagePrefix) {
			return
		}
		for index := range value.NumField() {
			if value.Type().Field(index).IsExported() {
				normalizeValue(value.Field(index))
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			if value.CanSet() {
				value.Set(reflect.MakeSlice(value.Type(), 0, 0))
			}
			return
		}
		for index := range value.Len() {
			normalizeValue(value.Index(index))
		}
	case reflect.Map:
		if value.IsNil() && value.CanSet() {
			value.Set(reflect.MakeMap(value.Type()))
		}
	}
}
