/** 个人 AI 员工暂停与恢复操作。 */
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { PersonalAgentPresence, pausePersonalAgent, resumePersonalAgent } from "@/api"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { usePersonalAgentInvalidator } from "@/hooks/use-personal-agent-invalidator"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"

/** 暂停或恢复个人 AI 员工，成功后刷新相关数据并提示结果。 */
export function usePersonalAgentPause() {
  const { t } = useTranslation("agents")
  const reportError = useRequestErrorReporter()
  const invalidate = usePersonalAgentInvalidator()
  const save = useImmediateSave()

  /** 按当前状态切换暂停，保存进行中时忽略重复操作。 */
  async function toggle(agent: { id: string; presence: PersonalAgentPresence }) {
    const request = save.begin()
    if (request === null) return
    const paused = agent.presence === PersonalAgentPresence.Paused
    try {
      await (paused ? resumePersonalAgent(agent.id) : pausePersonalAgent(agent.id))
      void invalidate(agent.id)
      if (save.isCurrent(request)) toast.success(t(paused ? "personal.pause.resumed" : "personal.pause.paused"))
    } catch (error) {
      if (!save.isCurrent(request)) return
      reportError(error, {
        log: "修改个人 AI 员工暂停状态",
        context: { agent_id: agent.id },
        fallback: t("personal.pause.error"),
      })
    } finally {
      save.finish(request)
    }
  }

  return { toggle, saving: save.saving }
}
