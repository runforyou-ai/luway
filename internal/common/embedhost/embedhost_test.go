package embedhost

import (
	"reflect"
	"testing"
)

// TestNormalize 验证网站主机配置的规范化和校验。
func TestNormalize(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "domain", value: " Example.COM. ", want: "example.com", ok: true},
		{name: "url", value: "https://Example.COM/help", want: "example.com", ok: true},
		{name: "wildcard", value: "*.Example.COM", want: "*.example.com", ok: true},
		{name: "subdomain shorthand", value: ".Example.COM:8443", want: ".example.com:8443", ok: true},
		{name: "all", value: "*", want: "*", ok: true},
		{name: "credentials", value: "https://user@example.com", ok: false},
		{name: "path without scheme", value: "example.com/help", ok: false},
		{name: "empty label", value: "example..com", ok: false},
		{name: "invalid port", value: "example.com:65536", ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Normalize(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("Normalize(%q) = %q, %v, want %q, %v", test.value, got, ok, test.want, test.ok)
			}
		})
	}
}

// TestNormalizeAll 验证网站主机列表去重和全量放行配置。
func TestNormalizeAll(t *testing.T) {
	got, ok := NormalizeAll([]string{"Example.COM", "", "example.com", "*.example.com"})
	want := []string{"example.com", "*.example.com"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAll() = %#v, %v, want %#v, true", got, ok, want)
	}

	got, ok = NormalizeAll([]string{"example.com", "*"})
	if !ok || !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("NormalizeAll() = %#v, %v, want [*], true", got, ok)
	}
}

// TestAllows 验证精确主机、端口和通配子域名匹配。
func TestAllows(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		host    string
		want    bool
	}{
		{name: "empty list", host: "example.com", want: true},
		{name: "all", allowed: []string{"*"}, host: "example.com", want: true},
		{name: "exact", allowed: []string{"example.com"}, host: "example.com", want: true},
		{name: "exact port", allowed: []string{"localhost:5173"}, host: "localhost:5173", want: true},
		{name: "wildcard child", allowed: []string{"*.example.com"}, host: "support.example.com", want: true},
		{name: "wildcard nested child", allowed: []string{".example.com"}, host: "a.b.example.com", want: true},
		{name: "wildcard root", allowed: []string{"*.example.com"}, host: "example.com", want: false},
		{name: "missing host", allowed: []string{"example.com"}, host: "", want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Allows(test.allowed, test.host); got != test.want {
				t.Fatalf("Allows(%#v, %q) = %v, want %v", test.allowed, test.host, got, test.want)
			}
		})
	}
}

// TestFrameAncestors 验证允许网站到嵌入策略的转换。
func TestFrameAncestors(t *testing.T) {
	if got := FrameAncestors(nil); got != "*" {
		t.Fatalf("FrameAncestors(nil) = %q", got)
	}
	if got := FrameAncestors([]string{"example.com", ".example.org"}); got != "example.com *.example.org" {
		t.Fatalf("FrameAncestors() = %q", got)
	}
}
