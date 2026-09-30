/** 助理在线状态的展示文案。 */
import type { TFunction } from "i18next"

import { AssistantPresence } from "@/api"

/** 返回助理在线状态的简短文案。 */
export function assistantPresenceLabel(presence: AssistantPresence, t: TFunction<"contacts">) {
  switch (presence) {
    case AssistantPresence.AssistantPresenceOnline:
      return t("assistants.presence.online")
    case AssistantPresence.AssistantPresencePaused:
      return t("assistants.presence.paused")
    case AssistantPresence.AssistantPresenceUnbound:
      return t("assistants.presence.unbound")
    case AssistantPresence.AssistantPresenceInactive:
      return t("assistants.presence.inactive")
    default:
      return t("assistants.presence.offline")
  }
}
