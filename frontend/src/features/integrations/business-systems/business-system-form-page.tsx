/** 业务系统新增与编辑页。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useParams, useSearchParams } from "react-router"

import { getBusinessSystem } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { BusinessSystemConnectionForm } from "@/features/integrations/business-systems/business-system-connection-form"
import { BusinessSystemTools } from "@/features/integrations/business-systems/business-system-tools"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

const listPath = "/business-systems"

/** 编辑页的页签，与 URL 的 tab 参数同步。 */
const editTabs = ["connection", "tools"] as const
type EditTab = (typeof editTabs)[number]

/** 新建页只填写连接；编辑页分连接与工具两个页签。 */
export function BusinessSystemFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation("integrations")
  const { businessSystemId = "" } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const detail = useResource(resourceKeys.businessSystem(businessSystemId), (signal) => getBusinessSystem(businessSystemId, signal), {
    enabled: mode === "edit",
    // 工具目录更新期间轮询详情。
    refetchInterval: (data) => (data?.toolsUpdating ? 1000 : false),
  })
  const requestedTab = searchParams.get("tab")
  const tabValid = editTabs.some((tab) => tab === requestedTab)
  const activeTab: EditTab = tabValid ? (requestedTab as EditTab) : "connection"

  // 编辑页地址缺少有效页签时补为连接页签。
  useEffect(() => {
    if (mode !== "edit" || tabValid) return
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", "connection")
    setSearchParams(nextParams, { replace: true })
  }, [mode, searchParams, setSearchParams, tabValid])

  /** 切换编辑页签并同步 URL。 */
  function setTab(value: string) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", value)
    setSearchParams(nextParams, { replace: true })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t(mode === "create" ? "businessSystem.form.createTitle" : "businessSystem.form.editTitle")}
        description={t(mode === "create" ? "businessSystem.form.createDescription" : "businessSystem.form.editDescription")}
        backTo={mode === "edit" ? listPath : undefined}
      />
      <PageContent variant="form">
        {mode === "create" ? (
          <BusinessSystemConnectionForm system={null} listPath={listPath} />
        ) : (
          <ResourceContent resources={detail} errorMessage={t("businessSystem.form.loadError")}>
            {detail.data ? (
              <Tabs value={activeTab} onValueChange={setTab}>
                <TabsList>
                  {editTabs.map((tab) => (
                    <TabsTrigger key={tab} value={tab}>
                      {t(`businessSystem.tabs.${tab}`)}
                    </TabsTrigger>
                  ))}
                </TabsList>
                <TabsContent value="connection" forceMount className="mt-6 data-[state=inactive]:hidden">
                  <BusinessSystemConnectionForm key={detail.data.id} system={detail.data} listPath={listPath} />
                </TabsContent>
                <TabsContent value="tools" forceMount className="mt-6 data-[state=inactive]:hidden">
                  <BusinessSystemTools system={detail.data} />
                </TabsContent>
              </Tabs>
            ) : null}
          </ResourceContent>
        )}
      </PageContent>
    </div>
  )
}
