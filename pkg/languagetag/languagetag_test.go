package languagetag

import "testing"

// TestNormalize 校验语言标签规范化与无效输入。
func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{"es": "es", "zh-cn": "zh-CN", " hi-latn ": "hi-Latn", "und": "und"} {
		got, ok := Normalize(input)
		if !ok || got != want {
			t.Fatalf("Normalize(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "not a tag", "x"} {
		if _, ok := Normalize(input); ok {
			t.Fatalf("Normalize(%q) should be invalid", input)
		}
	}
}

// TestSameAndReadable 校验语言比较与无语言内容的可读判断。
func TestSameAndReadable(t *testing.T) {
	if !Same("zh-CN", "zh-Hans") || !Same("zh", "zh-CN") || !Same("en", "en-US") || Same("zh-CN", "zh-TW") ||
		Same("hi-Latn", "hi") || Same("es", "en-US") || Same("", "") {
		t.Fatal("unexpected Same result")
	}
	if !Readable("und", "zh-CN") || !Readable("en", "en-US") || Readable("ja", "zh-CN") {
		t.Fatal("unexpected Readable result")
	}
}
