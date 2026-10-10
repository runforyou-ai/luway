//go:build server

package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/einorun/stream"
)

const runSnapshotLimit = 64 << 20
const runSnapshotChunk = 128 << 10
const runSnapshotTimeout = 5 * time.Second

// ErrRunSourceUnavailable 表示持久运行仍在执行，执行快照当前不可读取。
var ErrRunSourceUnavailable = errors.New("run source unavailable")

// runSnapshotRequest 指明执行尝试和本次请求的剩余时长，接收方从收到请求起计算截止时间。
type runSnapshotRequest struct {
	RunID   string
	Attempt int
	Timeout time.Duration
}

// runSnapshotPart 携带有界快照片段、完整长度和 SHA-256。
type runSnapshotPart struct {
	Index  int
	Count  int
	Size   int
	Digest string
	Data   []byte
	Error  string
}

// SetRunTransport 在接收请求前注入 Core NATS 与适配层事件发布器。
func (p *Publisher) SetRunTransport(nc *nats.Conn, prefix string, publish func(context.Context, string, RunStreamEvent) error) {
	p.nc, p.runPrefix, p.publishRun = nc, prefix+"_runs", publish
}

// ReadRunSnapshot 向持久记录指向的实例读取完整快照，同实例使用相同分块协议。
func (p *Publisher) ReadRunSnapshot(ctx context.Context, instanceID, runID string, attempt int) (*RunSnapshot, error) {
	if p == nil || p.nc == nil || instanceID == "" {
		return nil, ErrRunSourceUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, runSnapshotTimeout)
	defer cancel()
	inbox := nats.NewInbox()
	sub, err := p.nc.SubscribeSync(inbox)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRunSourceUnavailable, err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := sub.SetPendingLimits(1024, 2*runSnapshotLimit); err != nil {
		return nil, err
	}
	if err := p.nc.FlushWithContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRunSourceUnavailable, err)
	}
	deadline, _ := ctx.Deadline()
	request, _ := json.Marshal(runSnapshotRequest{RunID: runID, Attempt: attempt, Timeout: time.Until(deadline)}) //clock:local
	if err := p.nc.PublishRequest(p.runPrefix+".snapshot."+instanceID, inbox, request); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRunSourceUnavailable, err)
	}
	var data []byte
	var header runSnapshotPart
	for index := 0; ; index++ {
		msg, err := sub.NextMsgWithContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRunSourceUnavailable, err)
		}
		if len(msg.Data) > 2*runSnapshotChunk {
			return nil, errors.New("run snapshot part too large")
		}
		var part runSnapshotPart
		if err := json.Unmarshal(msg.Data, &part); err != nil {
			return nil, err
		}
		if part.Error != "" {
			return nil, fmt.Errorf("%w: %s", ErrRunSourceUnavailable, part.Error)
		}
		if index == 0 {
			if part.Size < 1 || part.Size > runSnapshotLimit || part.Count != (part.Size+runSnapshotChunk-1)/runSnapshotChunk {
				return nil, errors.New("invalid run snapshot bounds")
			}
			header = part
			data = make([]byte, 0, part.Size)
		}
		expected := min(runSnapshotChunk, header.Size-index*runSnapshotChunk)
		if part.Index != index || part.Count != header.Count || part.Size != header.Size || part.Digest != header.Digest || len(part.Data) != expected {
			return nil, errors.New("invalid run snapshot part")
		}
		data = append(data, part.Data...)
		if index+1 == header.Count {
			break
		}
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != header.Digest {
		return nil, errors.New("run snapshot digest mismatch")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot RunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.RunID != runID || snapshot.Attempt != attempt {
		return nil, stream.ErrMismatch
	}
	return &snapshot, nil
}

// serveRunSnapshot 捕获当前尝试的快照，并在请求截止前按有界片段回复。
func (p *Publisher) serveRunSnapshot(msg *nats.Msg) {
	var request runSnapshotRequest
	if len(msg.Data) > 1024 || json.Unmarshal(msg.Data, &request) != nil || msg.Reply == "" {
		return
	}
	if request.Timeout <= 0 || request.Timeout > runSnapshotTimeout+time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), request.Timeout)
	defer cancel()
	p.sourcesMu.Lock()
	source := p.sources[request.RunID]
	p.sourcesMu.Unlock()
	var snapshot *RunSnapshot
	if source != nil {
		source.mu.Lock()
		if !source.ended && source.snapshot.Attempt == request.Attempt {
			snapshot = new(source.snapshot.Clone())
		}
		source.mu.Unlock()
	}
	if snapshot == nil {
		_ = msg.Respond([]byte(`{"Error":"source unavailable"}`))
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > runSnapshotLimit {
		_ = msg.Respond([]byte(`{"Error":"snapshot exceeds 64 MiB"}`))
		return
	}
	digest := sha256.Sum256(data)
	count := (len(data) + runSnapshotChunk - 1) / runSnapshotChunk
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			return
		}
		part, err := json.Marshal(runSnapshotPart{Index: i, Count: count, Size: len(data), Digest: hex.EncodeToString(digest[:]), Data: data[i*runSnapshotChunk : min((i+1)*runSnapshotChunk, len(data))]})
		if err != nil || msg.Respond(part) != nil {
			return
		}
	}
}
