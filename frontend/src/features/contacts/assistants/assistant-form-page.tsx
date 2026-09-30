/** 助理独立创建页，以及分为基本信息与记忆两个页签的编辑页。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate, useParams, useSearchParams } from "react-router"

import { currentDevice, getAssistant, isNotFoundApiError, listDevices } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  AssistantCreateForm,
  AssistantEditForm,
} from "@/features/contacts/assistants/assistant-form"
import { useAssistantInvalidator } from "@/hooks/use-assistant-invalidator"
import { AssistantMemoryPanel } from "@/features/contacts/assistants/assistant-memory-panel"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

const listPath = "/contacts/assistants"

/** 在本机创建助理，或编辑当前成员名下的助理。 */
export function AssistantFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation("contacts")
  const { assistantId = "" } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const invalidate = useAssistantInvalidator()
  const local = useResource(resourceKeys.currentDevice(), () => currentDevice(), { enabled: mode === "create" })
  const devices = useResource(resourceKeys.devices(), () => listDevices(), { enabled: mode === "create" })
  const detail = useResource(resourceKeys.assistant(assistantId), () => getAssistant(assistantId), {
    enabled: mode === "edit",
  })
  const tab = searchParams.get("tab") === "memory" ? "memory" : "basic"
  const localDeviceID = local.data?.deviceId ?? ""
  const localDeviceRecord = devices.data?.devices.find((device) => device.id === localDeviceID)

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (mode !== "edit" || searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [mode, searchParams, setSearchParams, tab])

  // 助理不存在或不属于本人时返回列表。
  useEffect(() => {
    if (mode !== "edit" || !isNotFoundApiError(detail.error)) return
    console.warn("助理不存在", { assistant_id: assistantId })
    navigate(listPath, { replace: true })
  }, [assistantId, detail.error, mode, navigate])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={
          mode === "create"
            ? t("assistants.create")
            : detail.data
              ? t("assistants.edit", { name: detail.data.assistant.displayName })
              : t("assistants.editTitle")
        }
        description={t(mode === "create" ? "assistants.createDescription" : "assistants.editDescription")}
        backTo={mode === "edit" ? listPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? [detail] : [local, devices]}
          errorMessage={t("assistants.loadError")}
        >
          {mode === "create" ? (
            localDeviceID ? (
              <AssistantCreateForm
                deviceID={localDeviceID}
                deviceName={localDeviceRecord?.name ?? ""}
                localAgents={localDeviceRecord?.localAgents ?? []}
                onCancel={() => navigate(listPath)}
                onSaved={() => navigate(listPath, { replace: true })}
              />
            ) : (
              <p className="text-sm text-muted-foreground">{t("assistants.createOnDesktop")}</p>
            )
          ) : detail.data ? (
            <Tabs
              key={detail.data.assistant.id}
              value={tab}
              onValueChange={(value) => {
                const next = new URLSearchParams(searchParams)
                next.set("tab", value)
                setSearchParams(next, { replace: true })
              }}
            >
              <TabsList>
                <TabsTrigger value="basic">{t("assistants.tabs.basic")}</TabsTrigger>
                <TabsTrigger value="memory">{t("assistants.tabs.memory")}</TabsTrigger>
              </TabsList>
              <TabsContent value="basic" forceMount className="mt-6 data-[state=inactive]:hidden">
                <AssistantEditForm detail={detail.data} onSaved={() => void invalidate(assistantId)} />
              </TabsContent>
              <TabsContent value="memory" className="mt-6">
                <AssistantMemoryPanel assistantId={detail.data.assistant.id} />
              </TabsContent>
            </Tabs>
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}
