/** 业务系统调用。 */
import {
  BusinessSystemCredentialKind,
  BusinessSystemTransport,
  ContextValue,
  MCPServerType,
  OperationLevel,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 可绑定的可信上下文值，按界面展示顺序排列。 */
export const contextValues = [
  ContextValue.CustomerUserID,
  ContextValue.CustomerEmail,
  ContextValue.MemberUserID,
  ContextValue.MemberEmail,
  ContextValue.ConversationID,
] as const satisfies readonly ContextValue[]

/** 操作级别，由低到高排列。 */
export const operationLevels = [
  OperationLevel.L0,
  OperationLevel.L1,
  OperationLevel.L2,
  OperationLevel.L3,
] as const satisfies readonly OperationLevel[]

/** 业务系统的传输方式，按界面展示顺序排列。 */
export const businessSystemTransports = [
  BusinessSystemTransport.MCP,
  BusinessSystemTransport.HTTP,
] as const satisfies readonly BusinessSystemTransport[]

/** 业务系统的认证方式，按界面展示顺序排列。 */
export const businessSystemCredentialKinds = [
  BusinessSystemCredentialKind.BusinessSystemCredentialNone,
  BusinessSystemCredentialKind.BusinessSystemCredentialBearer,
  BusinessSystemCredentialKind.BusinessSystemCredentialHeader,
  BusinessSystemCredentialKind.BusinessSystemCredentialBasic,
] as const satisfies readonly BusinessSystemCredentialKind[]

/** MCP 服务的连接方式。 */
export const mcpServerTypes = [
  MCPServerType.StreamableHTTP,
  MCPServerType.SSE,
] as const satisfies readonly MCPServerType[]

/** 读取当前工作区的业务系统列表。 */
export const listBusinessSystems = ops.listBusinessSystems

/** 读取业务系统详情。 */
export const getBusinessSystem = ops.getBusinessSystem

/** 创建业务系统。 */
export const createBusinessSystem = ops.createBusinessSystem

/** 修改业务系统。 */
export const updateBusinessSystem = ops.updateBusinessSystem

/** 保存业务系统中一个工具的事实修正、停用与参数绑定，返回保存后的业务系统。 */
export const updateBusinessToolSetting = ops.updateBusinessToolSetting

/** 删除业务系统。 */
export const deleteBusinessSystem = ops.deleteBusinessSystem

/** 测试业务系统草稿连接配置。 */
export const testBusinessSystemConnection = ops.testBusinessSystemConnection

/** 测试已保存的业务系统连接。 */
export const testSavedBusinessSystemConnection = ops.testSavedBusinessSystemConnection

/** 提交全部业务系统的工具目录更新任务。 */
export const refreshBusinessSystemTools = ops.refreshBusinessSystemTools
