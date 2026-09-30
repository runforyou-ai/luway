/** 已保存 MCP 服务的列表行连接测试。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError, testSavedMCPServerConnection } from "@/api"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { usePendingIds } from "@/hooks/use-pending-ids"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 按服务分别记录连接测试状态，返回正在测试的服务和测试方法。 */
export function useMCPServerConnectionTest() {
  const { t } = useTranslation("integrations")
  const navigate = useNavigate()
  const testing = usePendingIds()
  const mounted = useMountedRef()

  /** 测试保存的配置并显示结果。 */
  async function test(serverId: string) {
    if (testing.pendingIds.has(serverId)) return
    await testing.run(serverId, async () => {
      try {
        await testSavedMCPServerConnection(serverId)
        if (mounted.current) toast.success(t("mcpServer.connection.success"))
      } catch (error) {
        if (!mounted.current || recoverSession(error, navigate)) return
        toast.error(isApiError(error) ? apiErrorMessage(error) : t("mcpServer.connection.error"))
      }
    })
  }

  return { testingIds: testing.pendingIds, test }
}
