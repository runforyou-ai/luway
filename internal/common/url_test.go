package common

import "testing"

// TestValidHTTPBaseURL 验证 API 端点地址只接受不含认证信息、查询参数和片段的 HTTP 地址。
func TestValidHTTPBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "https", value: "https://example.com/api/v1", valid: true},
		{name: "http", value: "http://localhost:5678", valid: true},
		{name: "credentials", value: "https://user:password@example.com", valid: false},
		{name: "query", value: "https://example.com?tenant=1", valid: false},
		{name: "fragment", value: "https://example.com#api", valid: false},
		{name: "unsupported scheme", value: "ftp://example.com", valid: false},
		{name: "relative", value: "/api/v1", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := ValidHTTPBaseURL(test.value); actual != test.valid {
				t.Fatalf("ValidHTTPBaseURL(%q) = %t, want %t", test.value, actual, test.valid)
			}
		})
	}
}

// TestValidHTTPURL 验证页面地址允许查询和片段，但仍拒绝认证信息和非 HTTP 协议。
func TestValidHTTPURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "https", value: "https://example.com/portal", valid: true},
		{name: "query and fragment", value: "https://example.com/portal?tenant=1#section", valid: true},
		{name: "credentials", value: "https://user:password@example.com", valid: false},
		{name: "unsupported scheme", value: "ftp://example.com", valid: false},
		{name: "relative", value: "/portal", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := ValidHTTPURL(test.value); actual != test.valid {
				t.Fatalf("ValidHTTPURL(%q) = %t, want %t", test.value, actual, test.valid)
			}
		})
	}
}
