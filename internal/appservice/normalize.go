package appservice

import "reflect"

// appservicePackage 限定切片归一化的遍历范围，遇到 time.Time 等外部类型即停止。
var appservicePackage = reflect.TypeFor[RequestMeta]().PkgPath()

// WithNormalizedSlices 归一化结果中的 nil 切片，供各出口方法包装调用结果。
func WithNormalizedSlices[T any](output T, err error) (T, error) {
	normalizeSlices(&output)
	return output, err
}

// normalizeSlices 把结果中的 nil 切片替换为空切片，使各端始终收到数组。
// output 必须是指向结果的指针。
func normalizeSlices(output any) {
	normalizeSliceValue(reflect.ValueOf(output))
}

// normalizeSliceValue 递归遍历指针、结构体和切片，就地补齐 nil 切片。
func normalizeSliceValue(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			normalizeSliceValue(value.Elem())
		}
	case reflect.Struct:
		if value.Type().PkgPath() != appservicePackage {
			return
		}
		for index := range value.NumField() {
			normalizeSliceValue(value.Field(index))
		}
	case reflect.Slice:
		if value.IsNil() {
			if value.CanSet() {
				value.Set(reflect.MakeSlice(value.Type(), 0, 0))
			}
			return
		}
		for index := range value.Len() {
			normalizeSliceValue(value.Index(index))
		}
	}
}
