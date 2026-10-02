/** 个人 AI 员工暂停与恢复操作。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  PersonalAgentPresence,
  isApiError,
  pausePersonalAgent,
  resumePersonalAgent,
} from "@/api"
import { usePersonalAgentInvalidator } from "@/hooks/use-personal-agent-invalidator"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 暂停或恢复个人 AI 员工，成功后刷新相关数据并提示结果。 */
export function usePersonalAgentPause() {
  const { t } = useTranslation("agents")
  const navigate = useNavigate()
  const invalidate = usePersonalAgentInvalidator()
  const save = useImmediateSave()

  /** 按当前状态切换暂停，保存进行中时忽略重复操作。 */
  async function toggle(agent: { id: string; presence: PersonalAgentPresence }) {
    const request = save.begin()
    if (request === null) return
    const paused = agent.presence === PersonalAgentPresence.PersonalAgentPresencePaused
    try {
      await (paused ? resumePersonalAgent(agent.id) : pausePersonalAgent(agent.id))
      void invalidate(agent.id)
      if (save.isCurrent(request)) toast.success(t(paused ? "personal.pause.resumed" : "personal.pause.paused"))
    } catch (error) {
      if (!save.isCurrent(request) || recoverSession(error, navigate)) return
      console.warn("修改个人 AI 员工暂停状态失败", { agent_id: agent.id, error })
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("personal.pause.error"))
    } finally {
      save.finish(request)
    }
  }

  return { toggle, saving: save.saving }
}
