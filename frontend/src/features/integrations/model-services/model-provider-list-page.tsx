/** 模型服务供应商列表页。 */
import { useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  deleteAIProvider,
  listAIProviders,
  type AIProviderSummaryData,
} from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { PageHeader } from "@/components/page-header"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { aiProviderBrandConfigs } from "@/features/integrations/model-services/model-provider-brands"
import { ModelProviderBrandDialog } from "@/features/integrations/model-services/model-provider-brand-dialog"
import { ModelProviderBrandIcon } from "@/features/integrations/model-services/model-provider-brand-icon"
import {
  modelTypeNameKeys,
  modelTypeOrder,
} from "@/features/integrations/model-services/model-service-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useResource } from "@/hooks/use-resource"

/** 显示企业配置的模型服务供应商及各类型模型数量。 */
export function ModelProviderListPage() {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const [choosingBrand, setChoosingBrand] = useState(false)
  const resource = useResource(resourceKeys.aiProviders(), () => listAIProviders())
  const providers = resource.data?.providers ?? []

  const deletion = useConfirmedAction<AIProviderSummaryData>({
    action: (provider) => deleteAIProvider(provider.id),
    invalidateKeys: (provider) => [
      resourceKeys.aiProviders(),
      resourceKeys.aiProvider(provider.id),
    ],
    logLabel: "模型服务供应商删除",
    successMessage: () => t("modelServices.delete.success"),
    errorMessage: () => t("modelServices.delete.error"),
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t("modelServices.title")}
        description={t("modelServices.description")}
      >
        <Button
          variant="subtle"
          size="icon-sm"
          aria-label={t("modelServices.list.create")}
          title={t("modelServices.list.create")}
          onClick={() => setChoosingBrand(true)}
        >
          <PlusIcon />
        </Button>
      </PageHeader>
      <ResourceListLayout
        resources={resource}
        errorMessage={t("modelServices.list.loadError")}
      >
        <ResourceTable
          columns={[
            {
              key: "provider",
              header: t("modelServices.list.columns.name"),
              cell: (provider) => {
                const brand = t(aiProviderBrandConfigs[provider.brand].nameKey)
                // 按模型类型统计数量，没有的类型不展示。
                const counts = modelTypeOrder
                  .map((type) => ({
                    type,
                    count: provider.models.filter((model) => model.type === type)
                      .length,
                  }))
                  .filter(({ count }) => count > 0)
                  .map(({ type, count }) =>
                    t("modelServices.list.modelCount", {
                      count,
                      type: t(modelTypeNameKeys[type]),
                    }),
                  )
                return (
                  <ResourceRowIdentity
                    leading={<ModelProviderBrandIcon brand={provider.brand} />}
                    name={provider.name}
                    secondary={provider.name === brand ? undefined : brand}
                    description={counts.join(" · ")}
                  />
                )
              },
            },
          ]}
          rows={providers}
          rowKey={(provider) => provider.id}
          empty={t("modelServices.list.empty")}
          onRowActivate={(provider) =>
            navigate(`/settings/model-services/${provider.id}`)
          }
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
        createPath={(brand) => `/settings/model-services/new/${brand}`}
      />
      <ConfirmationDialog
        {...deletion.dialog}
        title={
          deletion.item
            ? t("modelServices.delete.title", { name: deletion.item.name })
            : ""
        }
        description={t("modelServices.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
