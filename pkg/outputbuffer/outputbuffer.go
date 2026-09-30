// Package outputbuffer 并发收集进程输出，超出上限时只保留开头与结尾的指定字节数。
package outputbuffer

import (
	"strings"
	"sync"
)

// Buffer 并发收集输出，保留开头 headLimit 字节与最新的 tailLimit 字节，其余中间部分丢弃。
type Buffer struct {
	mu        sync.Mutex
	headLimit int
	tailLimit int
	head      []byte
	tail      []byte
	dropped   bool
}

// New 创建保留开头 head 字节与结尾 tail 字节的输出缓冲。
func New(head, tail int) *Buffer {
	return &Buffer{headLimit: head, tailLimit: tail}
}

// Write 追加一段输出，先填满开头部分，其余写入结尾部分并丢弃超出上限的较早内容。
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	if room := b.headLimit - len(b.head); room > 0 {
		n := min(room, len(p))
		b.head = append(b.head, p[:n]...)
		p = p[n:]
	}
	b.tail = append(b.tail, p...)
	if excess := len(b.tail) - b.tailLimit; excess > 0 {
		b.tail = append(b.tail[:0], b.tail[excess:]...)
		b.dropped = true
	}
	return written, nil
}

// String 返回保留的输出；开头与结尾之间有内容被省略时以提示行标出。
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.dropped || len(b.head) == 0 {
		return strings.ToValidUTF8(string(b.head)+string(b.tail), "�")
	}
	return strings.ToValidUTF8(string(b.head), "�") + "\n…（中间输出过长已省略）…\n" + strings.ToValidUTF8(string(b.tail), "�")
}

// Truncated 判断输出是否超出上限被省略。
func (b *Buffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
