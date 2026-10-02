/** 首次安装页。 */
import { useTranslation } from "react-i18next"

import { EntryLayout } from "@/components/entry-layout"
import { SetupForm } from "@/features/installation/setup-form"

/** 展示首次安装表单。 */
export function SetupPage() {
  const { t } = useTranslation("setup")
  return (
    <EntryLayout title={t("title")} description={t("description")}>
      <SetupForm />
    </EntryLayout>
  )
}
