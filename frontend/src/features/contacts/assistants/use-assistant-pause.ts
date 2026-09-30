/** 助理暂停与恢复操作。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AssistantPresence,
  isApiError,
  pauseAssistant,
  resumeAssistant,
  type AssistantData,
} from "@/api"
import { useAssistantInvalidator } from "@/hooks/use-assistant-invalidator"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 暂停或恢复助理，成功后刷新助理相关数据并提示结果。 */
export function useAssistantPause() {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  const invalidate = useAssistantInvalidator()
  const save = useImmediateSave()

  /** 按当前状态切换暂停，保存进行中时忽略重复操作。 */
  async function toggle(assistant: AssistantData) {
    const request = save.begin()
    if (request === null) return
    const paused = assistant.presence === AssistantPresence.AssistantPresencePaused
    try {
      await (paused ? resumeAssistant(assistant.id) : pauseAssistant(assistant.id))
      void invalidate(assistant.id)
      if (save.isCurrent(request)) toast.success(t(paused ? "assistants.pause.resumed" : "assistants.pause.paused"))
    } catch (error) {
      if (!save.isCurrent(request) || recoverSession(error, navigate)) return
      console.warn("修改助理暂停状态失败", { assistant_id: assistant.id, error })
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("assistants.pause.error"))
    } finally {
      save.finish(request)
    }
  }

  return { toggle, saving: save.saving }
}
