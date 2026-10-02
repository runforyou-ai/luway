/** 本机 Agent 在界面中的产品名称。 */
import { LocalAgentKind, type LocalAgentKindId } from "@/api"

/** 返回本机 Agent 的产品名称，产品名称不随界面语言变化。 */
export function localAgentName(kind: LocalAgentKindId) {
  switch (kind) {
    case LocalAgentKind.LocalAgentKindCodex:
      return "Codex"
  }
}
