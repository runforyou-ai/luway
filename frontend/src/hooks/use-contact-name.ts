/** 联系人在成员界面的展示名。 */
import { useCallback } from "react"
import { useTranslation } from "react-i18next"

/** 返回联系人展示名函数：有名称时使用名称，没有名称时显示访客编号，两者都没有时返回空串。 */
export function useContactName() {
  const { t } = useTranslation("contacts")
  return useCallback(
    (name: string | null | undefined, number: number | null | undefined) =>
      name?.trim() || (number == null ? "" : t("visitorNumber", { number })),
    [t],
  )
}
