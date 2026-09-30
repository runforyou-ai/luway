package common

import (
	"slices"
	"testing"

	"uuid"
)

// TestValidUUID 验证只接受规范 UUID。
func TestValidUUID(t *testing.T) {
	if !ValidUUID("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("canonical UUID should be accepted")
	}
	if !ValidUUID("123E4567-E89B-12D3-A456-426614174000") {
		t.Fatal("case-insensitive canonical UUID should be accepted")
	}
	if ValidUUID("urn:uuid:123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("URN UUID should not be accepted as a database identifier")
	}
	if ValidUUID("") {
		t.Fatal("empty value should not be accepted")
	}
}

// TestNormalizeUUID 验证 UUID 会去除空白并转换为规范形式。
func TestNormalizeUUID(t *testing.T) {
	normalized, valid := NormalizeUUID(" 123E4567-E89B-12D3-A456-426614174000 ")
	if !valid || normalized != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("NormalizeUUID() = %q, %v", normalized, valid)
	}
	if _, valid = NormalizeUUID("invalid"); valid {
		t.Fatal("invalid UUID should not be accepted")
	}
}

// TestNormalizeUUIDs 验证 UUID 列表会保持顺序并按规范值去重。
func TestNormalizeUUIDs(t *testing.T) {
	values, valid := NormalizeUUIDs([]string{
		"123E4567-E89B-12D3-A456-426614174000",
		" 123e4567-e89b-12d3-a456-426614174000 ",
		"123e4567-e89b-12d3-a456-426614174001",
	})
	want := []string{"123e4567-e89b-12d3-a456-426614174000", "123e4567-e89b-12d3-a456-426614174001"}
	if !valid || !slices.Equal(values, want) {
		t.Fatalf("NormalizeUUIDs() = %#v, %v", values, valid)
	}
	if _, valid = NormalizeUUIDs([]string{"invalid"}); valid {
		t.Fatal("list containing invalid UUID should not be accepted")
	}
}

// TestNewUUIDv5 验证命名空间内的确定性 UUID 与 RFC 9562 §5.5 一致。
func TestNewUUIDv5(t *testing.T) {
	namespace := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	first := NewUUIDv5(namespace, "1")
	if first.String() != "b04965e6-a9bb-591f-8f8a-1adcb2c8dc39" {
		t.Fatalf("NewUUIDv5() = %s", first)
	}
	if NewUUIDv5(namespace, "1") != first {
		t.Fatal("相同输入必须产生相同结果")
	}
	if second := NewUUIDv5(namespace, "2"); second.String() != "4b166dbe-d99d-5091-abdd-95b83330ed3a" {
		t.Fatalf("NewUUIDv5() = %s", second)
	}
}
