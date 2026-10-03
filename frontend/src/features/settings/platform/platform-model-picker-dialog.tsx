/** 平台模型来源中从供应商可提供的模型里选择上游模型的弹窗。 */
import { useId, useState } from "react"
import { useTranslation } from "react-i18next"

import { listPlatformAIProviderModels, type AIProviderModelData } from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { modelTypeNameKeys } from "@/features/integrations/model-services/model-service-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** providerId 非空时打开弹窗读取该供应商可提供的模型，按名称或标识搜索，选中后交给 onPick。 */
export function PlatformModelPickerDialog({
  providerId,
  onOpenChange,
  onPick,
}: {
  providerId: string
  onOpenChange: (open: boolean) => void
  onPick: (model: AIProviderModelData) => void
}) {
  const { t } = useTranslation(["platform", "integrations"])
  const [query, setQuery] = useState("")
  const searchID = useId()
  const models = useResource(
    resourceKeys.platformAIProviderModels(providerId),
    (signal) => listPlatformAIProviderModels(providerId, signal),
    { enabled: Boolean(providerId) },
  )
  const keyword = query.trim().toLocaleLowerCase()
  const matched = (models.data ?? []).filter(
    (model) =>
      !keyword ||
      model.name.toLocaleLowerCase().includes(keyword) ||
      model.identifier.toLocaleLowerCase().includes(keyword),
  )

  return (
    <Dialog
      open={Boolean(providerId)}
      onOpenChange={(open) => {
        if (!open) setQuery("")
        onOpenChange(open)
      }}
    >
      <DialogContent className="max-w-xl" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{t("platformModels.picker.title")}</DialogTitle>
        </DialogHeader>
        <ResourceContent resources={models} errorMessage={t("platformModels.picker.loadError")}>
          <div className="space-y-3">
            <Input
              id={searchID}
              value={query}
              aria-label={t("platformModels.picker.search")}
              onChange={(event) => setQuery(event.target.value)}
            />
            <ScrollArea className="h-80">
              {matched.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted-foreground">
                  {models.data?.length ? t("platformModels.picker.noMatches") : t("platformModels.picker.empty")}
                </p>
              ) : (
                <ul className="divide-y">
                  {matched.map((model) => (
                    <li key={model.identifier}>
                      <button
                        type="button"
                        className="flex w-full min-w-0 flex-col items-start gap-0.5 rounded-md px-3 py-2.5 text-left hover:bg-accent focus-visible:bg-accent focus-visible:outline-hidden"
                        onClick={() => {
                          setQuery("")
                          onPick(model)
                        }}
                      >
                        <span className="w-full truncate text-sm font-medium">
                          {model.name || model.identifier}
                          {model.type ? (
                            <span className="ml-2 text-xs font-normal text-muted-foreground">
                              {t(modelTypeNameKeys[model.type], { ns: "integrations" })}
                            </span>
                          ) : null}
                        </span>
                        <span className="w-full truncate font-mono text-xs text-muted-foreground">{model.identifier}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </ScrollArea>
          </div>
        </ResourceContent>
      </DialogContent>
    </Dialog>
  )
}
