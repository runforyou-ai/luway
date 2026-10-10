/** 展示整页读取失败的说明和重试入口。 */
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"

/** 整页居中展示读取失败说明，点击重试时调用 onRetry。 */
export function PageLoadError({ message, onRetry }: { message: string; onRetry: () => unknown }) {
  const { t } = useTranslation("common")
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center px-6 text-center">
      <p className="text-sm text-muted-foreground">{message}</p>
      <Button type="button" className="mt-4" variant="outline" onClick={() => void onRetry()}>
        {t("actions.retry")}
      </Button>
    </main>
  )
}
