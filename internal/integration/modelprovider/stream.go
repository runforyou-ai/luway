package modelprovider

import "github.com/cloudwego/eino/schema"

// PullStream 返回按需拉取分片的流：调用方每次读取时在其协程中调用 recv，recv 以 io.EOF 表示结束；调用方关闭读取端后立即调用一次 onClose。
func PullStream[T any](recv func() (T, error), onClose func()) *schema.StreamReader[T] {
	ticks, feeder := schema.Pipe[struct{}](0)
	// 发送端阻塞到调用方取走一次读取机会或关闭读取端，关闭后执行收尾。
	go func() {
		for !feeder.Send(struct{}{}, nil) {
		}
		onClose()
	}()
	return schema.StreamReaderWithConvert(ticks, func(struct{}) (T, error) { return recv() })
}
