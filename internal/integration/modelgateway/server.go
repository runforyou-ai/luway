package modelgateway

import (
	"context"
	"encoding/gob"
	"errors"
	"io"
	"net/http"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
)

// DecodeRequest 读取并解码网关请求体。
func DecodeRequest(body io.Reader) (Request, error) {
	var request Request
	if err := gob.NewDecoder(io.LimitReader(body, MaxRequestBytes)).Decode(&request); err != nil {
		return Request{}, err
	}
	return request, nil
}

// Serve 用 models 创建模型组件执行请求，并把输出逐帧写入响应；模型调用失败时以错误帧结束，写入失败时停止。
// 返回的错误都发生在写出响应之前，调用方据此返回错误响应。
func Serve(ctx context.Context, writer http.ResponseWriter, models agentruntime.ModelFactory, request Request) error {
	options, err := request.Options.ModelOptions()
	if err != nil {
		return err
	}
	chatModel, err := models(ctx, request.Model)
	if err != nil {
		return err
	}
	writer.Header().Set("Content-Type", ContentType)
	writer.WriteHeader(http.StatusOK)
	encoder := gob.NewEncoder(writer)
	flusher, _ := writer.(http.Flusher)
	send := func(frame Frame) error {
		if err := encoder.Encode(frame); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	if !request.Stream {
		message, err := chatModel.Generate(ctx, request.Messages, options...)
		if err != nil {
			_ = send(Frame{Error: err.Error()})
			return nil
		}
		_ = send(Frame{Message: message})
		return nil
	}
	stream, err := chatModel.Stream(ctx, request.Messages, options...)
	if err != nil {
		_ = send(Frame{Error: err.Error()})
		return nil
	}
	defer stream.Close()
	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			_ = send(Frame{Error: err.Error()})
			return nil
		}
		// 设备断开后停止读取上游输出，关闭流时调用记为已取消。
		if err := send(Frame{Message: message}); err != nil {
			return nil
		}
	}
}
