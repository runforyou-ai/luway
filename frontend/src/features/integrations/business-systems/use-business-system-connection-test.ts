/** 已保存业务系统的列表行连接测试。 */
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { testSavedBusinessSystemConnection } from "@/api"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { usePendingIds } from "@/hooks/use-pending-ids"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"

/** 按业务系统分别记录连接测试状态，返回正在测试的业务系统和测试方法。 */
export function useBusinessSystemConnectionTest() {
  const { t } = useTranslation("integrations")
  const reportError = useRequestErrorReporter()
  const testing = usePendingIds()
  const mounted = useMountedRef()

  /** 测试保存的配置并显示结果。 */
  async function test(businessSystemId: string) {
    if (testing.pendingIds.has(businessSystemId)) return
    await testing.run(businessSystemId, async () => {
      try {
        await testSavedBusinessSystemConnection(businessSystemId)
        if (mounted.current) toast.success(t("businessSystem.connection.success"))
      } catch (error) {
        if (!mounted.current) return
        reportError(error, { fallback: t("businessSystem.connection.error") })
      }
    })
  }

  return { testingIds: testing.pendingIds, test }
}
