package agentruntime

import (
	"sync"
	"time"

	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
)

// streamFlushInterval 是运行流增量的合并发布周期。
const streamFlushInterval = 50 * time.Millisecond

// streamPublisher 按固定周期合并运行流操作，并串行交给接收方。
type streamPublisher struct {
	header  runstream.Delta
	sink    func(runstream.Delta)
	mu      sync.Mutex
	pending []runstream.Operation
	stop    chan struct{}
	done    chan struct{}
}

// start 启动合并发布协程，未配置接收方时不发布。
func (p *streamPublisher) start() {
	if p.sink == nil {
		return
	}
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(streamFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.flush()
			case <-p.stop:
				p.flush()
				return
			}
		}
	}()
}

// close 发布剩余操作并等待发布协程退出。
func (p *streamPublisher) close() {
	if p.stop == nil {
		return
	}
	close(p.stop)
	<-p.done
}

// add 登记待发布的操作，模型执行只写入内存队列。
func (p *streamPublisher) add(operations ...runstream.Operation) {
	if p.sink == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = append(p.pending, operations...)
}

// flush 合并本周期的操作并发布为下一个序号的增量。
func (p *streamPublisher) flush() {
	p.mu.Lock()
	if len(p.pending) == 0 {
		p.mu.Unlock()
		return
	}
	delta := p.header
	delta.BaseSequence, delta.Sequence = p.header.Sequence, p.header.Sequence+1
	p.header.Sequence = delta.Sequence
	delta.Operations = runstream.MergeOperations(p.pending)
	p.pending = nil
	p.mu.Unlock()
	p.sink(delta)
}
