/** 个人 AI 员工在线状态的展示文案。 */
import type { TFunction } from "i18next"

import { PersonalAgentPresence } from "@/api"

/** 返回个人 AI 员工在线状态的简短文案。 */
export function personalAgentPresenceLabel(presence: PersonalAgentPresence, t: TFunction<"agents">) {
  switch (presence) {
    case PersonalAgentPresence.PersonalAgentPresenceOnline:
      return t("personal.presence.online")
    case PersonalAgentPresence.PersonalAgentPresencePaused:
      return t("personal.presence.paused")
    case PersonalAgentPresence.PersonalAgentPresenceUnbound:
      return t("personal.presence.unbound")
    case PersonalAgentPresence.PersonalAgentPresenceInactive:
      return t("personal.presence.inactive")
    default:
      return t("personal.presence.offline")
  }
}
