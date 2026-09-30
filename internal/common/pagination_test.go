package common

import "testing"

// TestNormalizePagination 验证分页默认值和每页数量上限。
func TestNormalizePagination(t *testing.T) {
	page, pageSize, valid := NormalizePagination(0, 0)
	if page != 1 || pageSize != 50 || !valid {
		t.Fatalf("NormalizePagination() = %d, %d, %v", page, pageSize, valid)
	}
	page, pageSize, valid = NormalizePagination(2, 101)
	if page != 2 || pageSize != 101 || valid {
		t.Fatalf("NormalizePagination() = %d, %d, %v", page, pageSize, valid)
	}
}
