/** 切换当前成员工作状态的共享流程。 */
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { updateUserWorkStatus, type WorkStatus } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"

/** 保存工作状态并刷新当前身份；与当前状态相同或保存进行中时忽略，changing 表示保存进行中。 */
export function useWorkStatusChange(current: WorkStatus) {
  const { t } = useTranslation("workspace")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [changing, setChanging] = useState(false)
  const pending = useRef(false)

  /** 保存选中的工作状态，失败时提示。 */
  async function change(workStatus: WorkStatus) {
    if (workStatus === current || pending.current) return
    pending.current = true
    setChanging(true)
    try {
      try {
        await updateUserWorkStatus({ workStatus })
      } catch (error) {
        if (!recoverSession(error, navigate)) {
          console.warn("切换工作状态失败", error)
          toast.error(t("workStatusUpdateError"))
        }
        return
      }
      // 状态已保存，身份刷新结果不影响本次切换。
      await invalidate(resourceKeys.identity()).catch((error: unknown) => {
        console.warn("刷新当前身份失败", error)
      })
    } finally {
      pending.current = false
      setChanging(false)
    }
  }

  return { changing, change }
}
