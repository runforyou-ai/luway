/** 整页居中的加载状态。 */
import { useTranslation } from "react-i18next"

import { LoadingIndicator } from "@/components/loading-indicator"

/** 整页居中展示加载状态，加载持续超过统一门槛后才显示。 */
export function PageLoading() {
  const { t } = useTranslation("common")
  return (
    <main className="flex min-h-dvh items-center justify-center">
      <LoadingIndicator>{t("status.loading")}</LoadingIndicator>
    </main>
  )
}
