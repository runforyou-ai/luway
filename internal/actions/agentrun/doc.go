//go:build server

// Package agentrun 实现 Agent 持久输入调度、业务执行指派、场景处理与事务收尾。
//
// 调度入口：schedule.go、customer_schedule.go 与 group_schedule.go 追加输入并投递运行。
// 执行入口：execute.go 认领运行、装配能力、调用运行时并收尾，input_feed.go 以 einorun.Feed 提供输入认领。
// 场景入口：customer_execute.go、agent_chat_execute.go、group_execute.go 与 copilot_execute.go 定义会话策略。
// 收尾入口：execute_complete.go 与 execute_fail.go 写入结果，customer_handoff.go 把客服运行收尾为转人工，rotation.go 推进运行轮次，journal.go 以 einorun.Journal 保存过程与恢复状态并读取恢复内容。
// 停止与取消：cancellation.go 与 stop_agent_reply.go 由 RunCancellation 结束在途运行，执行通道上的取消与输入结算由 actions/agentcancel 完成。run_scopes.go 为工具决定提供运行所属会话的锁定与事件唤醒。
// 平台托管运行的模型与工具循环由 internal/agentruntime 执行；工具确认与审批由 actions/tooldecision 处理，转人工承接与退回队列由 actions/servicehandoff 处理。
package agentrun
