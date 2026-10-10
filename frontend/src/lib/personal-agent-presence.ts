/** 个人 AI 员工在线状态的展示文案。 */
import type { TFunction } from "i18next"

import { PersonalAgentPresence } from "@/api"

/** 返回个人 AI 员工在线状态的简短文案。 */
export function personalAgentPresenceLabel(presence: PersonalAgentPresence, t: TFunction<"agents">) {
  switch (presence) {
    case PersonalAgentPresence.Online:
      return t("personal.presence.online")
    case PersonalAgentPresence.Paused:
      return t("personal.presence.paused")
    case PersonalAgentPresence.Unbound:
      return t("personal.presence.unbound")
    case PersonalAgentPresence.Inactive:
      return t("personal.presence.inactive")
    default:
      return t("personal.presence.offline")
  }
}
