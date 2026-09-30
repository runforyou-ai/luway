/** 助理在会话中的展示名格式化。 */
import { useCallback } from "react"
import { useTranslation } from "react-i18next"

/** 返回展示名格式化函数：助理显示为「主人的助理 · 名称」，其他身份原样返回名称。 */
export function useAssistantDisplayName() {
  const { t } = useTranslation("inbox")
  return useCallback(
    (name: string, ownerName: string | null | undefined) =>
      name && ownerName ? t("assistantDisplayName", { owner: ownerName, name }) : name,
    [t],
  )
}
