/** 企业互联网设置：按页签配置联网搜索与网页读取服务。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"

import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { WebFetchSettingsSection } from "@/features/integrations/web/web-fetch-settings-section"
import { WebSearchSettingsSection } from "@/features/integrations/web/web-search-settings-section"

/** 互联网设置页签，首项为缺省页签。 */
const webTabs = ["search", "fetch"] as const

type WebTab = (typeof webTabs)[number]

/** 按页签展示联网搜索与网页读取设置，页签与地址同步；两段表单常驻，切换时保留未保存的改动。 */
export function WebSettingsPage() {
  const { t } = useTranslation("integrations")
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = webTabs.find((value) => value === searchParams.get("tab")) ?? webTabs[0]

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("web.title")} description={t("web.description")} />
      <div className="app-page-gutter shrink-0">
        <Tabs
          value={tab}
          onValueChange={(value) => {
            const next = new URLSearchParams(searchParams)
            next.set("tab", value)
            setSearchParams(next, { replace: true })
          }}
        >
          <TabsList>
            {webTabs.map((value) => (
              <TabsTrigger key={value} value={value}>
                {t(`web.tabs.${value}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <PageContent variant="form">
        <WebTabContent tab={tab} />
      </PageContent>
    </div>
  )
}

/** 渲染当前页签内容；未选中的页签隐藏但仍挂载，保留表单状态。 */
function WebTabContent({ tab }: { tab: WebTab }) {
  return (
    <>
      <div className={tab === "search" ? undefined : "hidden"}>
        <WebSearchSettingsSection />
      </div>
      <div className={tab === "fetch" ? undefined : "hidden"}>
        <WebFetchSettingsSection />
      </div>
    </>
  )
}
