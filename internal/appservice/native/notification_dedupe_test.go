//go:build !server

package native

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestDeliveredNotificationsOncePerID 验证同一编号成功投递后不再投递，投递失败不登记、重试照常投递，空编号不去重。
func TestDeliveredNotificationsOncePerID(t *testing.T) {
	var delivered deliveredNotifications
	sends := 0
	send := func() error { sends++; return nil }
	failure := errors.New("通知服务不可用")
	if err := delivered.deliver("m1", func() error { sends++; return failure }); !errors.Is(err, failure) {
		t.Fatalf("首次投递失败应返回错误，err = %v", err)
	}
	if err := delivered.deliver("m1", send); err != nil || sends != 2 {
		t.Fatalf("失败后重试应照常投递，sends = %d, err = %v", sends, err)
	}
	if err := delivered.deliver("m1", send); err != nil || sends != 2 {
		t.Fatalf("成功投递后不应重复投递，sends = %d, err = %v", sends, err)
	}
	for range 2 {
		if err := delivered.deliver("", send); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 4 {
		t.Fatalf("空编号不应去重，sends = %d", sends)
	}
}

// TestDeliveredNotificationsConcurrent 验证同一编号并发投递时只有一方真正投递；先投递的一方失败时，等待的一方重新投递。
func TestDeliveredNotificationsConcurrent(t *testing.T) {
	var delivered deliveredNotifications
	var sends atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() {
		_ = delivered.deliver("m1", func() error {
			sends.Add(1)
			close(started)
			<-release
			return errors.New("通知服务不可用")
		})
	})
	<-started
	var waiterErr error
	group.Go(func() {
		waiterErr = delivered.deliver("m1", func() error { sends.Add(1); return nil })
	})
	close(release)
	group.Wait()
	if waiterErr != nil || sends.Load() != 2 {
		t.Fatalf("先投递失败后等待方应重新投递，sends = %d, err = %v", sends.Load(), waiterErr)
	}
	if err := delivered.deliver("m1", func() error { sends.Add(1); return nil }); err != nil || sends.Load() != 2 {
		t.Fatalf("成功投递后不应重复投递，sends = %d", sends.Load())
	}
}
