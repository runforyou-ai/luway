/** 按服务对象展示设置，当前页签与地址同步。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import { ServiceAudience } from "@/api"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { BusinessHoursSettings } from "@/features/settings/business-hours-form"
import { CustomerIdentitySettings } from "@/features/settings/customer-identity-settings"
import { ContactProfileSettings } from "@/features/settings/contact-profile-settings"
import { ServiceCategoriesSettings } from "@/features/settings/service-categories-settings"
import { ServiceSummarySettings } from "@/features/settings/service-summary-settings"
import { ServiceTimeoutsSettings } from "@/features/settings/service-timeouts-form"
import { TranslationSettings } from "@/features/settings/translation-settings"

import { ServicePolicyAudienceContext } from "./service-policy-context"

/** 服务设置的页签，首项为缺省页签。 */
const customerServiceTabs = ["businessHours", "assignment", "categories", "contactProfile", "summary", "translation", "identity"] as const

/** 按地址中的页签显示工作时间、分配与提醒、咨询分类、客户资料、会话小结、翻译或客户身份验证设置。 */
export function ServicePolicySettings({ audience }: { audience: ServiceAudience }) {
  const { t } = useTranslation("settings")
  const [searchParams, setSearchParams] = useSearchParams()
  const tabs = customerServiceTabs.filter(
    (value) => audience === ServiceAudience.Customer || (value !== "identity" && value !== "contactProfile" && value !== "translation"),
  )
  const tab =
    tabs.find((value) => value === searchParams.get("tab")) ??
    tabs[0]

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  return (
    <ServicePolicyAudienceContext value={audience}>
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
          {audience === ServiceAudience.Customer ? (
            <TabsTrigger value="contactProfile">
            {t("customerService.tabs.contactProfile")}
          </TabsTrigger>
          ) : null}
          <TabsTrigger value="summary">
            {t("customerService.tabs.summary")}
          </TabsTrigger>
          {audience === ServiceAudience.Customer ? (
            <TabsTrigger value="translation">{t("customerService.tabs.translation")}</TabsTrigger>
          ) : null}
          {audience === ServiceAudience.Customer ? (
            <TabsTrigger value="identity">
            {t("customerService.tabs.identity")}
          </TabsTrigger>
          ) : null}
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
        {audience === ServiceAudience.Customer ? (
          <TabsContent
          value="contactProfile"
          forceMount
          className="mt-6 data-[state=inactive]:hidden"
        >
          <ContactProfileSettings active={tab === "contactProfile"} />
        </TabsContent>
        ) : null}
        <TabsContent
          value="summary"
          forceMount
          className="mt-6 data-[state=inactive]:hidden"
        >
          <ServiceSummarySettings />
        </TabsContent>
        {audience === ServiceAudience.Customer ? (
          <TabsContent
            value="translation"
            forceMount
            className="mt-6 data-[state=inactive]:hidden"
          >
            <TranslationSettings />
          </TabsContent>
        ) : null}
        {audience === ServiceAudience.Customer ? (
          <TabsContent
          value="identity"
          forceMount
          className="mt-6 data-[state=inactive]:hidden"
        >
          <CustomerIdentitySettings />
        </TabsContent>
        ) : null}
      </Tabs>
    </ServicePolicyAudienceContext>
  )
}
