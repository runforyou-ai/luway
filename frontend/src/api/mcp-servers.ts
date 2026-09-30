/** MCP 服务调用。 */
import {
  CreateMCPServer,
  DeleteMCPServer,
  GetMCPServer,
  ListMCPServers,
  UpdateMCPServer,
  UpdateMCPToolPurpose,
  TestMCPServerConnection,
  TestSavedMCPServerConnection,
  RefreshMCPServerTools,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type {
  MCPServer,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type MCPServerData = NonNullArrays<MCPServer>

/** 读取当前企业的 MCP 服务列表。 */
export const listMCPServers = bind(ListMCPServers)

/** 读取 MCP 服务详情。 */
export const getMCPServer = bind(GetMCPServer)

/** 创建 MCP 服务。 */
export const createMCPServer = bind(CreateMCPServer)

/** 修改 MCP 服务。 */
export const updateMCPServer = bind(UpdateMCPServer)

/** 标记 MCP 服务中一个工具的用途，用途为空表示取消标记。 */
export const updateMCPToolPurpose = bind(UpdateMCPToolPurpose)

/** 删除 MCP 服务。 */
export const deleteMCPServer = bind(DeleteMCPServer)

/** 测试 MCP 草稿连接配置。 */
export const testMCPServerConnection = bind(TestMCPServerConnection)

/** 测试已保存的 MCP 服务连接。 */
export const testSavedMCPServerConnection = bind(TestSavedMCPServerConnection)

/** 提交全部 MCP 服务的工具更新任务。 */
export const refreshMCPServerTools = bind(RefreshMCPServerTools)
