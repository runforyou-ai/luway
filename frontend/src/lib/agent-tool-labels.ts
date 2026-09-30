/** Agent 工具的共享界面文案映射。 */
import type { TFunction } from "i18next"

/** 返回工具代码对应的显示名称，没有对应文案的工具显示工具代码。 */
export function agentToolLabel(tool: string, t: TFunction<"common">) {
  switch (tool) {
    case "search_knowledge":
      return t("agentTools.searchKnowledge")
    case "web_search":
      return t("agentTools.webSearch")
    case "web_fetch":
      return t("agentTools.webFetch")
    case "search_customer_history":
      return t("agentTools.searchCustomerHistory")
    case "ask_customer":
      return t("agentTools.askCustomer")
    case "handoff_to_human":
      return t("agentTools.handoffToHuman")
    case "resolve_conversation":
      return t("agentTools.resolveConversation")
    case "mcp":
      return t("agentTools.mcp")
    case "ls":
      return t("agentTools.listFolder")
    case "read_file":
      return t("agentTools.readFile")
    case "glob":
      return t("agentTools.findFiles")
    case "grep":
      return t("agentTools.searchFiles")
    case "write_file":
      return t("agentTools.writeFile")
    case "edit_file":
      return t("agentTools.editFile")
    case "delete_file":
      return t("agentTools.deleteFile")
    case "execute":
      return t("agentTools.runCommand")
    case "skill":
      return t("agentTools.useSkill")
    case "install_skill":
      return t("agentTools.installSkill")
    case "remove_skill":
      return t("agentTools.removeSkill")
    case "agent":
      return t("agentTools.delegate")
    default:
      return tool
  }
}
