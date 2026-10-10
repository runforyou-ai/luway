/** 模型服务表单中的模型目录列表。 */
import { ArrowDownUpIcon, BinaryIcon, MessageSquareTextIcon, SplitIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { AIModelType, type AIModelInputModality } from "@/api"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import {
  parseTokenCount,
  type AIModelFormValues,
} from "@/components/model-provider/model-provider-schema"
import {
  modelInputModalityNameKeys,
  modelTypeNameKeys,
} from "@/components/model-provider/model-service-options"

/** 模型类型在行首显示的图标。 */
const modelTypeIcons: Record<AIModelType, typeof MessageSquareTextIcon> = {
  [AIModelType.Chat]: MessageSquareTextIcon,
  [AIModelType.Embedding]: BinaryIcon,
  [AIModelType.Rerank]: ArrowDownUpIcon,
  [AIModelType.Decision]: SplitIcon,
}

/** 逐行展示模型名称、标识、类型、输入类型和 Token 上限，缺少 Token 上限的模型标记待补充，点击行编辑模型；removable 为 false 时删除不可用。 */
export function ModelProviderModelList({
  models,
  removable,
  onEdit,
  onRemove,
}: {
  models: { key: string; model: AIModelFormValues }[]
  removable: boolean
  onEdit: (index: number) => void
  onRemove: (index: number) => void
}) {
  const { t } = useTranslation(["integrations", "common"])
  const rows = models.map((row, index) => ({ ...row, index }))

  return (
    <ResourceListFrame>
      <ResourceTable
        columns={[
          {
            key: "model",
            header: t("modelServices.models.columns.name"),
            cell: ({ model }) => {
              const type = model.type as AIModelType
              const isChat = type === AIModelType.Chat
              const incomplete =
                parseTokenCount(model.contextWindow) === null ||
                (isChat && parseTokenCount(model.maxOutputTokens) === null)
              // 补充说明依次为类型、输入类型、上下文窗口和对话模型的最大输出，未填写的项不展示。
              const details = [
                t(modelTypeNameKeys[type]),
                model.inputModalities
                  .map((modality) => t(modelInputModalityNameKeys[modality as AIModelInputModality]))
                  .join(t("modelServices.models.modalitySeparator")),
                model.contextWindow
                  ? t("modelServices.models.contextWindowSummary", {
                      value: model.contextWindow,
                    })
                  : "",
                isChat && model.maxOutputTokens
                  ? t("modelServices.models.maxOutputTokensSummary", {
                      value: model.maxOutputTokens,
                    })
                  : "",
              ].filter(Boolean)
              return (
                <ResourceRowIdentity
                  icon={modelTypeIcons[type]}
                  name={model.name}
                  secondary={<span className="font-mono text-xs">{model.identifier}</span>}
                  badge={
                    incomplete ? (
                      <StatusBadge variant="destructive" showDot={false}>
                        {t("modelServices.models.incomplete")}
                      </StatusBadge>
                    ) : undefined
                  }
                  description={details.join(" · ")}
                />
              )
            },
          },
        ]}
        rows={rows}
        rowKey={(row) => row.key}
        empty={t("modelServices.models.empty")}
        onRowActivate={(row) => onEdit(row.index)}
        rowActions={(row) => [
          {
            key: "edit",
            label: t("common:actions.edit"),
            onSelect: () => onEdit(row.index),
          },
          {
            key: "delete",
            label: t("common:actions.delete"),
            destructive: true,
            separatorBefore: true,
            disabled: !removable,
            onSelect: () => onRemove(row.index),
          },
        ]}
      />
    </ResourceListFrame>
  )
}
