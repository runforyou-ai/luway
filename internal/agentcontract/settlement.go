package agentcontract

import (
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// interruptedReplayableResult 是可重新执行的调用中断后交给模型的结果。
	interruptedReplayableResult = "执行被中断，没有返回结果，需要时可以重新调用。"
	// interruptedResult 是不可重新执行、没有外部副作用的调用中断后交给模型的结果。
	interruptedResult = "执行被中断，结果未知。"
	// needsReviewResult 是有外部副作用的调用中断后交给模型的结果。
	needsReviewResult = "执行被中断，外部操作的实际结果未知，已交由人工核对，不要重复执行。"
)

// ErrComputerOffline 是电脑未连接时派发或尚未领取的调用交给模型的失败原因。
var ErrComputerOffline = errors.New("电脑未连接，操作没有执行。请告诉用户打开这台电脑上的应用并保持联网后再试。")

// ComputerLost 返回电脑离线或撤销时派发给它且未结束调用的结算：尚未领取的调用失败，已领取的调用按可重新执行与外部副作用中断或待核对；
// 依次返回状态、交给模型的结果与失败原因。
func ComputerLost(claimed, replayable, sideEffects bool) (domain.AgentToolCallStatus, *string, *string) {
	if !claimed {
		return domain.AgentToolCallFailed, nil, new(ErrComputerOffline.Error())
	}
	status, result := InterruptedStatus(replayable, sideEffects)
	return status, &result, nil
}

// InterruptedStatus 按可重新执行与外部副作用返回已开始而未完成的调用的结算状态与交给模型的结果，执行中断、运行结束与电脑丢失共用。
func InterruptedStatus(replayable, sideEffects bool) (domain.AgentToolCallStatus, string) {
	switch {
	case sideEffects && !replayable:
		return domain.AgentToolCallNeedsReview, needsReviewResult
	case replayable:
		return domain.AgentToolCallInterrupted, interruptedReplayableResult
	}
	return domain.AgentToolCallInterrupted, interruptedResult
}

// ErrDecisionUnavailable 由日志在需要确认或审批的调用找不到处理成员时返回，调用按失败交给模型。
var ErrDecisionUnavailable = errors.New("没有可以确认或审批这项操作的成员，操作没有执行。")
