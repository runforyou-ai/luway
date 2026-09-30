package appservice

import (
	"reflect"
	"testing"
	"time"
)

// TestNormalizeSlicesFillsNestedNilSlices 验证嵌套结构体、指针和切片元素中的 nil 切片都被补齐，遍历在包外类型处停止。
func TestNormalizeSlicesFillsNestedNilSlices(t *testing.T) {
	type leaf struct {
		Tags []string
	}
	type node struct {
		Leaf     leaf
		Pointer  *leaf
		Empty    *leaf
		External time.Time
		Children []leaf
		Existing []string
	}

	value := node{Pointer: &leaf{}, Children: []leaf{{}, {Tags: []string{"kept"}}}, Existing: []string{"kept"}}
	normalizeSlices(&value)

	if value.Leaf.Tags == nil || value.Pointer.Tags == nil {
		t.Fatalf("嵌套结构体和指针中的 nil 切片未补齐: %+v", value)
	}
	if value.Empty != nil {
		t.Fatalf("nil 指针不应被填充: %+v", value.Empty)
	}
	if value.Children[0].Tags == nil {
		t.Fatalf("切片元素中的 nil 切片未补齐: %+v", value.Children[0])
	}
	if len(value.Children[1].Tags) != 1 || len(value.Existing) != 1 {
		t.Fatalf("已有切片内容被改动: %+v", value)
	}
	if !value.External.IsZero() {
		t.Fatalf("包外类型不应被遍历改写: %+v", value.External)
	}
}

// TestServiceOutputsDeclareNoNilSlices 验证 Service 每个带结果的方法都归一化了返回值。
func TestServiceOutputsDeclareNoNilSlices(t *testing.T) {
	serviceType := reflect.TypeOf(&Service{})
	for index := range serviceType.NumMethod() {
		method := serviceType.Method(index)
		if method.Type.NumOut() != 2 {
			continue
		}
		output := reflect.New(method.Type.Out(0))
		normalizeSlices(output.Interface())
		if path := firstNilSlice(output.Elem(), method.Type.Out(0).Name()); path != "" {
			t.Fatalf("%s 的结果类型仍存在无法补齐的 nil 切片: %s", method.Name, path)
		}
	}
}

// firstNilSlice 返回第一个仍为 nil 的切片字段路径，全部补齐时返回空串。
func firstNilSlice(value reflect.Value, path string) string {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			return firstNilSlice(value.Elem(), path)
		}
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Type().Field(index)
			if found := firstNilSlice(value.Field(index), path+"."+field.Name); found != "" {
				return found
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			return path
		}
	}
	return ""
}
