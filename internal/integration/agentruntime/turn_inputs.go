package agentruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const triggerPollInterval = 200 * time.Millisecond

// errEmptyFinalResponse 表示模型本轮没有产出可发布的正文。
var errEmptyFinalResponse = errors.New("agent returned an empty final response")

// turnInputs 统一管理持久输入的投递、认领边界和循环停止决策。
type turnInputs struct {
	feed        InputFeed
	loop        *adk.TurnLoop[Trigger, *schema.AgenticMessage]
	holdPreempt func() bool // 返回 true 时新输入只入队不抢占，用于保持已固定的转人工决定。

	mu           sync.Mutex
	maxPushedSeq int64
	claimedSeq   int64
	closed       bool
}

// run 投递初始输入并等待循环和输入监听全部退出。
func (i *turnInputs) run(ctx context.Context) error {
	if err := i.poll(ctx, false); err != nil {
		return err
	}
	if i.maxPushedSeq == 0 {
		return errors.New("agent run has no pending trigger")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	i.loop.Run(runCtx)
	watcherDone := make(chan error, 1)
	go func() {
		watcherDone <- i.watch(runCtx)
	}()
	exit := i.loop.Wait()
	cancel()
	if err := <-watcherDone; err != nil {
		return err
	}
	return exit.ExitReason
}

// watch 定期投递新增输入并在收尾后保留运行结果。
func (i *turnInputs) watch(ctx context.Context) error {
	ticker := time.NewTicker(triggerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := i.poll(ctx, true); err != nil {
				i.mu.Lock()
				defer i.mu.Unlock()
				if i.closed || ctx.Err() != nil {
					return nil
				}
				i.closed = true
				i.loop.Stop(adk.WithImmediate())
				return err
			}
		}
	}
}

// poll 从持久输入流读取新信号，并串行完成去重和抢占投递。
func (i *turnInputs) poll(ctx context.Context, preempt bool) error {
	i.mu.Lock()
	afterSeq, closed := i.maxPushedSeq, i.closed
	i.mu.Unlock()
	if closed {
		return nil
	}
	triggers, err := i.feed.Peek(ctx, afterSeq)
	if err != nil {
		return err
	}
	var ack <-chan struct{}
	i.mu.Lock()
	if !i.closed {
		for _, trigger := range triggers {
			if trigger.Seq <= i.maxPushedSeq {
				continue
			}
			var accepted bool
			if preempt && ack == nil && (i.holdPreempt == nil || !i.holdPreempt()) {
				accepted, ack = i.loop.Push(trigger, adk.WithPreempt[Trigger, *schema.AgenticMessage](adk.AnySafePoint))
			} else {
				accepted, _ = i.loop.Push(trigger)
			}
			if !accepted {
				break
			}
			i.maxPushedSeq = trigger.Seq
		}
	}
	i.mu.Unlock()
	if ack != nil {
		// 确认取消信号已提交，工具结束钩子返回后才会进入下一个安全点。
		<-ack
	}
	return nil
}

// claim 认领本轮输入并记录已消费的边界。
func (i *turnInputs) claim(ctx context.Context, throughSeq int64) (ClaimedInput, error) {
	claimed, err := i.feed.Claim(ctx, throughSeq)
	if err != nil {
		return ClaimedInput{}, err
	}
	if claimed.EndSeq <= 0 || len(claimed.Messages) == 0 {
		return ClaimedInput{}, errors.New("agent input feed returned no claimed messages")
	}
	i.mu.Lock()
	i.claimedSeq = claimed.EndSeq
	i.maxPushedSeq = max(i.maxPushedSeq, claimed.EndSeq)
	i.mu.Unlock()
	return claimed, nil
}

// finish 在锁内根据已投递序号决定是否收尾；转人工决定不可降级，直接收尾，之后到达的输入留给人工处理。
func (i *turnInputs) finish(ctx context.Context, turn *adk.TurnContext[Trigger, *schema.AgenticMessage], decision TerminalDecision, content string) (bool, error) {
	if decision.Kind == domain.AgentRunOutcomeHandoff {
		i.mu.Lock()
		defer i.mu.Unlock()
		if !i.closed {
			i.closed = true
			i.loop.Stop()
		}
		return true, nil
	}
	if pending, err := i.pending(ctx, turn); pending || err != nil {
		return false, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.maxPushedSeq > i.claimedSeq {
		return false, nil
	}
	if content == "" {
		return false, errEmptyFinalResponse
	}
	i.closed = true
	i.loop.Stop()
	return true, nil
}

// pending 判断本轮是否已被抢占或有尚未认领的新输入，是则当前候选作废并进入下一轮。
func (i *turnInputs) pending(ctx context.Context, turn *adk.TurnContext[Trigger, *schema.AgenticMessage]) (bool, error) {
	select {
	case <-turn.Preempted:
		return true, nil
	default:
	}
	if err := i.poll(ctx, false); err != nil {
		return false, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closed || i.maxPushedSeq > i.claimedSeq, nil
}

// rerun 投递一次不认领持久输入的纠正信号，让循环在当前边界内重新执行。
func (i *turnInputs) rerun() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil
	}
	if accepted, _ := i.loop.Push(Trigger{Correction: true}); !accepted {
		return errors.New("agent turn loop rejected grounding correction")
	}
	return nil
}
