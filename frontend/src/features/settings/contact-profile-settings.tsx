/** 客户资料设置的资料字段与客户标签子页签。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ContactFieldsSettings } from "@/features/settings/contact-fields-settings"
import { ContactTagsSettings } from "@/features/settings/contact-tags-settings"

/** 按地址切换资料字段与客户标签，保留各页签的内容状态。 */
export function ContactProfileSettings({ active }: { active: boolean }) {
  const { t } = useTranslation("settings")
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = searchParams.get("profile") === "tags" ? "tags" : "fields"

  // 客户资料页签打开时，将缺省或无效的子页签写回地址。
  useEffect(() => {
    if (!active || searchParams.get("profile") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("profile", tab)
    setSearchParams(next, { replace: true })
  }, [active, searchParams, setSearchParams, tab])

  return (
    <Tabs
      value={tab}
      onValueChange={(value) => {
        const next = new URLSearchParams(searchParams)
        next.set("profile", value)
        setSearchParams(next, { replace: true })
      }}
    >
      <TabsList
        aria-label={t("customerService.tabs.contactProfile")}
        className="w-fit gap-1 rounded-lg border-0 bg-muted p-1"
      >
        <TabsTrigger
          value="fields"
          className="mb-0 rounded-md border-0 px-3 py-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm"
        >
          {t("customerService.contactFields.title")}
        </TabsTrigger>
        <TabsTrigger
          value="tags"
          className="mb-0 rounded-md border-0 px-3 py-1.5 data-[state=active]:bg-background data-[state=active]:shadow-sm"
        >
          {t("customerService.contactTags.title")}
        </TabsTrigger>
      </TabsList>
      <TabsContent value="fields" forceMount className="mt-6 data-[state=inactive]:hidden">
        <ContactFieldsSettings />
      </TabsContent>
      <TabsContent value="tags" forceMount className="mt-6 data-[state=inactive]:hidden">
        <ContactTagsSettings />
      </TabsContent>
    </Tabs>
  )
}
