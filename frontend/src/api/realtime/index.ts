/** 导出共用账号连接的成员通知与运行过程客户端。 */
export { realtimeClient, workspaceActivityClient } from "@/api/realtime/member-connection"
import { RunStreamClient } from "@/api/realtime/run-stream"
export type { RealtimeClientEvent, RealtimeState } from "@/api/realtime/realtime-client"
export type { RunStreamEvent, RunStreamState } from "@/api/realtime/run-stream"
export type { RunStreamBlock, RunStreamPlanTask, RunStreamToolCall } from "@/api/generated/contract"

/** 创建指定运行的过程流客户端，调用方负责建立与关闭。 */
export function createRunStreamClient(runID: string) { return new RunStreamClient(runID) }
