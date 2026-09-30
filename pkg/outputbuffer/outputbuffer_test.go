package outputbuffer

import (
	"strings"
	"testing"
)

// TestBufferKeepsHeadAndTail 验证输出超出上限时保留开头与结尾并标记裁剪。
func TestBufferKeepsHeadAndTail(t *testing.T) {
	const limit = 1024
	output := New(limit/2, limit/2)
	output.Write([]byte("HEAD"))
	output.Write([]byte(strings.Repeat("x", limit)))
	output.Write([]byte("TAIL"))
	text := output.String()
	if !output.Truncated() || !strings.HasPrefix(text, "HEAD") || !strings.HasSuffix(text, "TAIL") || !strings.Contains(text, "已省略") || len(text) > limit+100 {
		t.Fatalf("truncated=%v len=%d", output.Truncated(), len(text))
	}
}

// TestBufferKeepsOnlyTail 验证开头上限为 0 时只保留最后的字节且不插入提示行。
func TestBufferKeepsOnlyTail(t *testing.T) {
	output := New(0, 4)
	output.Write([]byte("abcdef"))
	output.Write([]byte("gh"))
	if text := output.String(); text != "efgh" || !output.Truncated() {
		t.Fatalf("text=%q truncated=%v", text, output.Truncated())
	}
}
