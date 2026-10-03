/** 平台设置的平台供应商页：列出为平台模型提供服务的供应商及其近 24 小时的上游失败率，添加、进入编辑或删除。 */
import { useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { deletePlatformAIProvider, listPlatformAIProviders, type PlatformAIProviderSummaryData } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { ModelProviderBrandDialog } from "@/features/integrations/model-services/model-provider-brand-dialog"
import { ModelProviderBrandIcon } from "@/features/integrations/model-services/model-provider-brand-icon"
import { aiProviderBrandConfigs } from "@/features/integrations/model-services/model-provider-brands"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useReportFormat } from "@/hooks/use-report-format"
import { useResource } from "@/hooks/use-resource"

/** 平台供应商列表路径。 */
export const platformProviderListPath = "/settings/platform/providers"

/** 列出平台供应商、使用它的平台模型数与近 24 小时的上游失败率，有失败时显示最近一次原因；删除经确认后执行。 */
export function PlatformProviderListPage() {
  const { t } = useTranslation(["platform", "integrations", "common"])
  const { rate } = useReportFormat()
  const navigate = useNavigate()
  const [choosingBrand, setChoosingBrand] = useState(false)
  // 每次进入重新读取，失败率取最新数据。
  const providers = useResource(resourceKeys.platformAIProviders(), (signal) => listPlatformAIProviders(signal), { staleTime: 0 })
  const deletion = useConfirmedAction<PlatformAIProviderSummaryData>({
    action: (provider) => deletePlatformAIProvider(provider.id),
    invalidateKeys: (provider) => [resourceKeys.platformAIProviders(), resourceKeys.platformAIProvider(provider.id)],
    successMessage: () => t("platformProviders.delete.success"),
    errorMessage: () => t("platformProviders.delete.error"),
    logLabel: "平台供应商删除",
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("platformProviders.title")} description={t("platformProviders.description")}>
        <Button
          variant="subtle"
          size="icon-sm"
          aria-label={t("platformProviders.create")}
          title={t("platformProviders.create")}
          onClick={() => setChoosingBrand(true)}
        >
          <PlusIcon />
        </Button>
      </PageHeader>
      <ResourceListLayout resources={providers} errorMessage={t("platformProviders.loadError")}>
        <ResourceTable<PlatformAIProviderSummaryData>
          columns={[
            {
              key: "provider",
              header: t("platformProviders.title"),
              cellClassName: "min-w-0",
              cell: (provider) => {
                const brand = t(aiProviderBrandConfigs[provider.brand].nameKey, { ns: "integrations" })
                const usage =
                  provider.modelCount > 0
                    ? t("platformProviders.modelCount", { count: provider.modelCount })
                    : t("platformProviders.unused")
                return (
                  <ResourceRowIdentity
                    leading={<ModelProviderBrandIcon brand={provider.brand} />}
                    name={provider.name}
                    secondary={provider.name === brand ? undefined : brand}
                    description={
                      provider.lastError ? (
                        <span className="min-w-0 truncate" title={provider.lastError}>{t("platformProviders.lastError", { usage, error: provider.lastError })}</span>
                      ) : (
                        usage
                      )
                    }
                  />
                )
              },
            },
            {
              key: "health",
              header: t("platformProviders.healthColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
              cell: (provider) =>
                provider.recentAttempts > 0
                  ? t("platformProviders.failureRate", { rate: rate(provider.recentFailures, provider.recentAttempts) })
                  : t("platformProviders.noRecentCalls"),
            },
          ]}
          rows={providers.data ?? []}
          rowKey={(provider) => provider.id}
          empty={t("platformProviders.empty")}
          onRowActivate={(provider) => navigate(`${platformProviderListPath}/${provider.id}`)}
          rowActions={(provider) => [
            {
              key: "delete",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => deletion.select(provider),
            },
          ]}
        />
      </ResourceListLayout>

      <ModelProviderBrandDialog
        open={choosingBrand}
        onOpenChange={setChoosingBrand}
        createPath={(brand) => `${platformProviderListPath}/new/${brand}`}
      />
      <ConfirmationDialog
        {...deletion.dialog}
        title={deletion.item ? t("platformProviders.delete.title", { name: deletion.item.name }) : ""}
        description={t("platformProviders.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
