package modelprovider

import "testing"

// TestCompatibleBaseURL 验证百炼与 Ollama 地址使用各自的 OpenAI 兼容入口。
func TestCompatibleBaseURL(t *testing.T) {
	tests := []struct {
		brand string
		value string
		want  string
	}{
		{brand: "alibaba", value: "https://dashscope.aliyuncs.com", want: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{brand: "ollama", value: "http://localhost:11434", want: "http://localhost:11434/v1"},
		{brand: "ollama", value: "http://localhost:11434/v1", want: "http://localhost:11434/v1"},
		{brand: "openai_compatible", value: "http://127.0.0.1:8000/v1/", want: "http://127.0.0.1:8000/v1"},
	}
	for _, test := range tests {
		got, err := CompatibleBaseURL(test.brand, test.value)
		if err != nil || got != test.want {
			t.Fatalf("CompatibleBaseURL(%q, %q) = %q, error = %v", test.brand, test.value, got, err)
		}
	}
}
