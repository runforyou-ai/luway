/** 平台设置的平台模型页：列出对全部工作区可用的平台模型及其来源与积分价格，添加、进入编辑或删除。 */
import { BrainCircuitIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { deletePlatformAIModel, listPlatformAIModels, type PlatformAIModelData } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { modelTypeNameKeys } from "@/features/integrations/model-services/model-service-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useCreditFormat } from "@/hooks/use-credit-format"
import { useResource } from "@/hooks/use-resource"

/** 平台模型列表路径。 */
export const platformModelListPath = "/settings/platform/models"

/** 列出平台模型：主行为名称与类型，第二行为首个已启用来源与来源数，价格独立成列；没有已启用来源或未定价的模型标记工作区不可用的原因。 */
export function PlatformModelListPage() {
  const { t } = useTranslation(["platform", "integrations", "common"])
  const credit = useCreditFormat()
  const navigate = useNavigate()
  const models = useResource(resourceKeys.platformAIModels(), (signal) => listPlatformAIModels(signal))
  const deletion = useConfirmedAction<PlatformAIModelData>({
    action: (model) => deletePlatformAIModel(model.id),
    invalidateKeys: (model) => [
      resourceKeys.platformAIModels(),
      resourceKeys.platformAIModel(model.id),
      resourceKeys.platformAIProviders(),
    ],
    successMessage: () => t("platformModels.delete.success"),
    errorMessage: () => t("platformModels.delete.error"),
    logLabel: "平台模型删除",
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("platformModels.title")} description={t("platformModels.description")}>
        <Button
          variant="subtle"
          size="icon-sm"
          aria-label={t("platformModels.create")}
          title={t("platformModels.create")}
          onClick={() => navigate(`${platformModelListPath}/new`)}
        >
          <PlusIcon />
        </Button>
      </PageHeader>
      <ResourceListLayout resources={models} errorMessage={t("platformModels.loadError")}>
        <ResourceTable<PlatformAIModelData>
          columns={[
            {
              key: "model",
              header: t("platformModels.title"),
              cellClassName: "min-w-0",
              cell: (model) => {
                const primary = model.routes.find((route) => route.enabled)
                return (
                  <ResourceRowIdentity
                    icon={BrainCircuitIcon}
                    name={model.name}
                    secondary={t(modelTypeNameKeys[model.type], { ns: "integrations" })}
                    badge={
                      !primary ? (
                        <StatusBadge variant="muted">{t("platformModels.unavailable")}</StatusBadge>
                      ) : !model.price ? (
                        <StatusBadge variant="muted">{t("platformModels.unpriced")}</StatusBadge>
                      ) : undefined
                    }
                    description={[
                      primary ? `${primary.providerName} · ${primary.identifier}` : null,
                      t("platformModels.routeCount", { count: model.routes.length }),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  />
                )
              },
            },
            {
              key: "price",
              header: t("platformModels.form.price"),
              cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
              cell: (model) => (model.price ? credit.price(model.price, model.type) : null),
            },
          ]}
          rows={models.data ?? []}
          rowKey={(model) => model.id}
          empty={t("platformModels.empty")}
          onRowActivate={(model) => navigate(`${platformModelListPath}/${model.id}`)}
          rowActions={(model) => [
            {
              key: "delete",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => deletion.select(model),
            },
          ]}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...deletion.dialog}
        title={deletion.item ? t("platformModels.delete.title", { name: deletion.item.name }) : ""}
        description={t("platformModels.delete.description")}
        pendingLabel={t("common:actions.deleting")}
      />
    </div>
  )
}
