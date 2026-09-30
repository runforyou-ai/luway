package agentruntime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type gatedInputFeed struct {
	*testInputFeed
	peeks   atomic.Int32
	started chan struct{}
	release chan struct{}
	late    bool
}

// Peek 控制第一次查询与收尾查询的完成顺序，模拟数据库查询交错。
func (f *gatedInputFeed) Peek(ctx context.Context, _ int64) ([]Trigger, error) {
	first := f.peeks.Add(1) == 1
	if first {
		close(f.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.release:
		}
	}
	if first == f.late {
		return []Trigger{{Seq: 2}}, nil
	}
	return nil, nil
}

// TestTurnInputsOrdersPollingAndFinish 验证输入投递与收尾顺序，以及收尾后的投递序号稳定性。
func TestTurnInputsOrdersPollingAndFinish(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "input-before-finish"
		if late {
			name = "query-after-finish"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			feed := &gatedInputFeed{started: make(chan struct{}), release: make(chan struct{}), late: late}
			inputs := &turnInputs{feed: feed, maxPushedSeq: 1, claimedSeq: 1}
			inputs.loop = adk.NewTurnLoop(adk.TurnLoopConfig[Trigger, *schema.AgenticMessage]{
				GenInput: func(context.Context, *adk.TurnLoop[Trigger, *schema.AgenticMessage], []Trigger) (*adk.GenInputResult[Trigger, *schema.AgenticMessage], error) {
					panic("test must not run the loop")
				},
				PrepareAgent: func(context.Context, *adk.TurnLoop[Trigger, *schema.AgenticMessage], []Trigger) (adk.TypedAgent[*schema.AgenticMessage], error) {
					panic("test must not run the agent")
				},
			})
			turn := &adk.TurnContext[Trigger, *schema.AgenticMessage]{Loop: inputs.loop}
			done := make(chan error, 1)
			var finished bool
			go func() {
				if late {
					done <- inputs.poll(ctx, false)
				} else {
					var err error
					finished, err = inputs.finish(ctx, turn, TerminalDecision{}, "旧回答")
					done <- err
				}
			}()
			select {
			case <-feed.started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var err error
			if late {
				finished, err = inputs.finish(ctx, turn, TerminalDecision{}, "旧回答")
			} else {
				err = inputs.poll(ctx, false)
			}
			close(feed.release)
			pollErr := <-done
			if err != nil || pollErr != nil {
				t.Fatalf("finish error = %v, poll error = %v", err, pollErr)
			}
			if finished != late || inputs.closed != late {
				t.Fatalf("finished = %v, closed = %v, late = %v", finished, inputs.closed, late)
			}
			wantSeq := int64(2)
			if late {
				wantSeq = 1
			}
			if inputs.maxPushedSeq != wantSeq {
				t.Fatalf("pushed sequence = %d, want %d", inputs.maxPushedSeq, wantSeq)
			}
		})
	}
}
