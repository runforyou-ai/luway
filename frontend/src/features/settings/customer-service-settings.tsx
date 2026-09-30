/** 客服设置页签：工作时间、分配提醒、咨询分类、客户资料、会话小结、翻译与客户身份验证，当前页签与地址同步。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { BusinessHoursSettings } from "@/features/settings/business-hours-form"
import { CustomerIdentitySettings } from "@/features/settings/customer-identity-settings"
import { ContactFieldsSettings } from "@/features/settings/contact-fields-settings"
import { ContactTagsSettings } from "@/features/settings/contact-tags-settings"
import { ServiceCategoriesSettings } from "@/features/settings/service-categories-settings"
import { ServiceSummarySettings } from "@/features/settings/service-summary-settings"
import { ServiceTimeoutsSettings } from "@/features/settings/service-timeouts-form"
import { TranslationSettings } from "@/features/settings/translation-settings"

/** 客服设置的页签，首项为缺省页签。 */
const customerServiceTabs = ["businessHours", "assignment", "categories", "contactProfile", "summary", "translation", "identity"] as const

/** 按地址中的页签显示工作时间、分配与提醒、咨询分类、会话小结、翻译或客户身份验证设置。 */
export function CustomerServiceSettings() {
  const { t } = useTranslation("settings")
  const [searchParams, setSearchParams] = useSearchParams()
  const tab =
    customerServiceTabs.find((value) => value === searchParams.get("tab")) ??
    customerServiceTabs[0]

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  return (
    <Tabs
      value={tab}
      onValueChange={(value) => {
        const next = new URLSearchParams(searchParams)
        next.set("tab", value)
        setSearchParams(next, { replace: true })
      }}
    >
      <TabsList>
        <TabsTrigger value="businessHours">
          {t("customerService.tabs.businessHours")}
        </TabsTrigger>
        <TabsTrigger value="assignment">
          {t("customerService.tabs.assignment")}
        </TabsTrigger>
        <TabsTrigger value="categories">
          {t("customerService.tabs.categories")}
        </TabsTrigger>
        <TabsTrigger value="contactProfile">
          {t("customerService.tabs.contactProfile")}
        </TabsTrigger>
        <TabsTrigger value="summary">
          {t("customerService.tabs.summary")}
        </TabsTrigger>
        <TabsTrigger value="translation">
          {t("customerService.tabs.translation")}
        </TabsTrigger>
        <TabsTrigger value="identity">
          {t("customerService.tabs.identity")}
        </TabsTrigger>
      </TabsList>
      <TabsContent
        value="businessHours"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <BusinessHoursSettings />
      </TabsContent>
      <TabsContent
        value="assignment"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <ServiceTimeoutsSettings />
      </TabsContent>
      <TabsContent
        value="categories"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <ServiceCategoriesSettings />
      </TabsContent>
      <TabsContent
        value="contactProfile"
        forceMount
        className="mt-6 space-y-10 data-[state=inactive]:hidden"
      >
        <ContactFieldsSettings />
        <ContactTagsSettings />
      </TabsContent>
      <TabsContent
        value="summary"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <ServiceSummarySettings />
      </TabsContent>
      <TabsContent
        value="translation"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <TranslationSettings />
      </TabsContent>
      <TabsContent
        value="identity"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <CustomerIdentitySettings />
      </TabsContent>
    </Tabs>
  )
}
