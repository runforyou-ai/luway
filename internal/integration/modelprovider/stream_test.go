package modelprovider

import (
	"errors"
	"io"
	"testing"
	"time"
)

// TestPullStreamReadsOnDemandAndClosesPromptly 验证流按读取拉取分片、以 io.EOF 结束，且调用方关闭后立即收尾一次，期间不再拉取。
func TestPullStreamReadsOnDemandAndClosesPromptly(t *testing.T) {
	pulls := 0
	closed := make(chan struct{})
	stream := PullStream(func() (int, error) {
		pulls++
		if pulls > 2 {
			return 0, io.EOF
		}
		return pulls, nil
	}, func() { close(closed) })
	for want := 1; want <= 2; want++ {
		if got, err := stream.Recv(); err != nil || got != want {
			t.Fatalf("recv = %d, %v", got, err)
		}
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("recv after end = %v", err)
	}
	select {
	case <-closed:
		t.Fatal("读取端未关闭时执行了收尾")
	case <-time.After(20 * time.Millisecond):
	}

	// 调用方读取一个分片后关闭，收尾立即执行且不再拉取。
	blocked := 0
	stopped := make(chan struct{})
	early := PullStream(func() (int, error) {
		blocked++
		return blocked, nil
	}, func() { close(stopped) })
	if _, err := early.Recv(); err != nil {
		t.Fatal(err)
	}
	early.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("关闭读取端后未执行收尾")
	}
	if blocked != 1 {
		t.Fatalf("pulls = %d", blocked)
	}
	stream.Close()
	<-closed
}
