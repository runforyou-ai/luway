/** 个人 AI 员工在会话中的展示名格式化。 */
import { useCallback } from "react"
import { useTranslation } from "react-i18next"

/** 返回展示名格式化函数：个人 AI 员工显示为「负责人的 AI 员工 · 名称」，其他身份原样返回名称。 */
export function usePersonalAgentDisplayName() {
  const { t } = useTranslation("inbox")
  return useCallback(
    (name: string, responsibleName: string | null | undefined) =>
      name && responsibleName ? t("personalAgentDisplayName", { responsible: responsibleName, name }) : name,
    [t],
  )
}
