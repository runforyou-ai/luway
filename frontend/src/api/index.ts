/** 前端业务 API 入口，聚合各业务域模块并导出契约类型。 */
export * from "@/api/generated/contract"
export { ApiError, isApiError, isNotFoundApiError, serverURL } from "@/api/client"
export { connectServer, install, login, logout, probeServer, register } from "@/api/auth"
export { getSyncHeads, loadIdentity, loadInstallationStatus, loadStartup, sessionPath, type ConnectReason, type Startup } from "@/api/session"
export {
  createRunStreamClient,
  realtimeClient,
  workspaceActivityClient,
  type RealtimeClientEvent,
  type RealtimeState,
  type RunStreamBlock,
  type RunStreamEvent,
  type RunStreamPlanTask,
  type RunStreamState,
  type RunStreamToolCall,
} from "@/api/realtime"
export {
  completeFileUpload,
  createFilePartUpload,
  FileTransfer,
  cancelFileUpload,
  uploadFileSlice,
  createFileUpload,
  uploadFile,
} from "@/api/uploads"
export * from "@/api/agents"
export * from "@/api/product-docs"
export * from "@/api/agent-evaluations"
export * from "@/api/personal-agents"
export * from "@/api/ai-providers"
export * from "@/api/business-systems"
export * from "@/api/web-search"
export * from "@/api/channels"
export * from "@/api/contacts"
export * from "@/api/platform"
export * from "@/api/computers"
export * from "@/api/seats"
export * from "@/api/wechat"
export * from "@/api/inbox"
export * from "@/api/conversations"
export * from "@/api/conversation-files"
export * from "@/api/group-conversations"
export * from "@/api/service-sessions"
export * from "@/api/invitations"
export * from "@/api/knowledge-bases"
export * from "@/api/knowledge-gaps"
export * from "@/api/tool-decisions"
export * from "@/api/reports"
export * from "@/api/roles"
export * from "@/api/settings"
export * from "@/api/translations"
export * from "@/api/teams"
export * from "@/api/users"
export * from "@/api/workspaces"
