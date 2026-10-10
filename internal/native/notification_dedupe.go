//go:build !server

package native

import (
	"sync"
	"time"
)

// notificationDedupeWindow 是同一通知编号成功投递后不再重复投递的时长。
const notificationDedupeWindow = 10 * time.Minute

// deliveredNotifications 记录近期成功投递的通知编号，同一进程内多处观察到同一条消息时只投递一次。
type deliveredNotifications struct {
	mu        sync.Mutex
	delivered map[string]time.Time
	// inflight 按编号记录正在投递的通知，投递结束时关闭。
	inflight map[string]chan struct{}
}

// deliver 投递一条通知：编号在去重时长内已成功投递时直接返回；同一编号正在投递时等待其结束，成功则跳过，失败则由本次重新投递；投递失败不登记编号，调用方的重试照常投递；空编号不去重。
func (d *deliveredNotifications) deliver(id string, send func() error) error {
	if id == "" {
		return send()
	}
	for {
		d.mu.Lock()
		if d.delivered == nil {
			d.delivered, d.inflight = map[string]time.Time{}, map[string]chan struct{}{}
		}
		now := time.Now()
		for previous, at := range d.delivered {
			if now.Sub(at) > notificationDedupeWindow {
				delete(d.delivered, previous)
			}
		}
		if _, ok := d.delivered[id]; ok {
			d.mu.Unlock()
			return nil
		}
		if pending, ok := d.inflight[id]; ok {
			d.mu.Unlock()
			<-pending
			continue
		}
		done := make(chan struct{})
		d.inflight[id] = done
		d.mu.Unlock()

		err := send()
		d.mu.Lock()
		delete(d.inflight, id)
		if err == nil {
			d.delivered[id] = time.Now()
		}
		close(done)
		d.mu.Unlock()
		return err
	}
}
