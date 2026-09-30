/** 模型服务表单中读取可选模型并批量加入目录的入口和弹窗。 */
import { useId, useState } from "react"
import { SearchIcon } from "lucide-react"
import { useWatch, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  discoverAIProviderModels,
  isApiError,
  listAvailableAIModels,
  type AIProviderBrandId,
  type AIProviderModelData,
} from "@/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { aiProviderBrandConfigs } from "@/features/integrations/model-services/model-provider-brands"
import { modelFormValue } from "@/features/integrations/model-services/model-provider-model-values"
import type { AIProviderFormValues } from "@/features/integrations/model-services/model-provider-schema"
import {
  modelInputModalityNameKeys,
  modelTypeNameKeys,
  modelTypeOrder,
} from "@/features/integrations/model-services/model-service-options"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 读取当前品牌的可选模型，按类型分组搜索和勾选，确认后把选中的模型交给表单追加；目录中已有的模型标记为已添加。 */
export function ModelPickerDialog({
  form,
  onAppend,
}: {
  form: UseFormReturn<AIProviderFormValues>
  onAppend: (models: AIProviderFormValues["models"]) => void
}) {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [availableModels, setAvailableModels] = useState<AIProviderModelData[]>([])
  const [draftModelIDs, setDraftModelIDs] = useState<Set<string>>(new Set())
  const [addedModelIDs, setAddedModelIDs] = useState<Set<string>>(new Set())
  const [query, setQuery] = useState("")
  const searchID = useId()
  const [loadingModels, setLoadingModels] = useState(false)
  const mounted = useMountedRef()
  const watchedBrand = useWatch({ control: form.control, name: "brand" }) as AIProviderBrandId
  const discoversModels = Boolean(aiProviderBrandConfigs[watchedBrand].discoversModels)

  /** 读取当前品牌的可选模型并打开选择弹窗。 */
  async function openModelDialog() {
    if (loadingModels) return
    const brand = form.getValues("brand") as AIProviderBrandId
    // 模型目录由服务实例提供时，先校验连接配置再读取实例。
    const discovers = Boolean(aiProviderBrandConfigs[brand].discoversModels)
    if (discovers) {
      const valid = await form.trigger(["brand", "credentialType", "apiKey", "apiUrl"], {
        shouldFocus: true,
      })
      if (!valid || !mounted.current) return
    }
    setLoadingModels(true)
    const { credentialType, apiKey, apiUrl } = form.getValues()
    const requested = `${brand}\n${credentialType}\n${apiKey}\n${apiUrl}`
    try {
      const models = discovers
        ? await discoverAIProviderModels({ brand, credentialType, apiKey, apiUrl })
        : await listAvailableAIModels(brand)
      if (!mounted.current) return
      // 读取期间连接配置变化时结果已过期，不打开选择弹窗。
      const current = form.getValues()
      if (
        requested !==
        `${current.brand}\n${current.credentialType}\n${current.apiKey}\n${current.apiUrl}`
      ) {
        return
      }
      setAvailableModels(models)
      setAddedModelIDs(
        new Set(current.models.map((model) => model.identifier.trim())),
      )
      setDraftModelIDs(new Set())
      setQuery("")
      setOpen(true)
    } catch (requestError) {
      if (!mounted.current) return
      if (recoverSession(requestError, navigate)) return
      console.warn("可选模型加载失败", { brand, error: requestError })
      toast.error(
        isApiError(requestError)
          ? apiErrorMessage(requestError, ["brand", "credentialType", "apiKey", "apiUrl"])
          : t(
              discovers
                ? "modelServices.models.discoverError"
                : "modelServices.models.loadError",
            ),
      )
    } finally {
      if (mounted.current) setLoadingModels(false)
    }
  }

  /** 切换弹窗中的待选模型。 */
  function toggleDraftModel(identifier: string, checked: boolean) {
    setDraftModelIDs((current) => {
      const next = new Set(current)
      if (checked) next.add(identifier)
      else next.delete(identifier)
      return next
    })
  }

  /** 追加选中的模型。 */
  function confirmModels() {
    onAppend(
      availableModels
        .filter((model) => draftModelIDs.has(model.identifier))
        .map(modelFormValue),
    )
    setOpen(false)
  }

  // 按名称或标识过滤后按模型类型分组。
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const matchedModels = availableModels.filter(
    (model) =>
      model.name.toLocaleLowerCase().includes(normalizedQuery) ||
      model.identifier.toLocaleLowerCase().includes(normalizedQuery),
  )
  const groups = modelTypeOrder
    .map((type) => ({
      type,
      models: matchedModels.filter((model) => model.type === type),
    }))
    .filter((group) => group.models.length > 0)

  return (
    <>
      <Button
        type="button"
        variant="link"
        size="sm"
        className="h-auto p-0"
        disabled={loadingModels}
        onClick={() => void openModelDialog()}
      >
        {loadingModels
          ? t("modelServices.models.loading")
          : t(
              discoversModels
                ? "modelServices.models.discover"
                : "modelServices.models.fetch",
            )}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="max-w-2xl"
          aria-describedby={undefined}
        >
          <DialogHeader>
            <DialogTitle>
              {t(
                discoversModels
                  ? "modelServices.models.discoverDialogTitle"
                  : "modelServices.models.dialogTitle",
              )}
            </DialogTitle>
          </DialogHeader>
          <div className="grid min-h-0 gap-2">
            <label htmlFor={searchID} className="sr-only">
              {t("modelServices.models.search")}
            </label>
            <div className="relative">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                id={searchID}
                value={query}
                autoComplete="off"
                className="pl-9"
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <ScrollArea className="h-[clamp(8rem,calc(100svh-17rem),20rem)] rounded-md border">
              {groups.length === 0 ? (
                <p className="px-6 py-12 text-center text-sm text-muted-foreground">
                  {normalizedQuery
                    ? t("modelServices.models.noMatches")
                    : t("modelServices.models.dialogEmpty")}
                </p>
              ) : (
                <div className="grid gap-3 p-1.5">
                  {groups.map((group) => (
                    <div
                      key={group.type}
                      role="group"
                      aria-label={t(modelTypeNameKeys[group.type])}
                    >
                      <p className="px-3 pt-1.5 pb-1 text-xs font-medium text-muted-foreground">
                        {t(modelTypeNameKeys[group.type])}
                      </p>
                      {group.models.map((model) => {
                        const added = addedModelIDs.has(model.identifier)
                        return (
                          <label
                            key={model.identifier}
                            className="flex items-center gap-3 rounded-md px-3 py-2 transition-colors hover:bg-muted has-disabled:hover:bg-transparent"
                          >
                            <input
                              type="checkbox"
                              className="size-4 accent-primary disabled:opacity-60"
                              checked={added || draftModelIDs.has(model.identifier)}
                              disabled={added}
                              onChange={(event) =>
                                toggleDraftModel(model.identifier, event.target.checked)
                              }
                              aria-label={t("modelServices.models.toggle", {
                                name: model.name,
                              })}
                            />
                            <span className="grid min-w-0 flex-1 gap-0.5 leading-tight">
                              <span className="truncate text-sm">
                                {model.name}
                                {model.name !== model.identifier ? (
                                  <span className="ml-1.5 font-mono text-xs text-muted-foreground">
                                    {model.identifier}
                                  </span>
                                ) : null}
                              </span>
                              <span className="truncate text-xs text-muted-foreground">
                                {model.inputModalities
                                  .map((modality) =>
                                    t(modelInputModalityNameKeys[modality]),
                                  )
                                  .join(t("modelServices.models.modalitySeparator"))}
                              </span>
                            </span>
                            {added ? (
                              <span className="shrink-0 text-xs text-muted-foreground">
                                {t("modelServices.models.added")}
                              </span>
                            ) : null}
                          </label>
                        )
                      })}
                    </div>
                  ))}
                </div>
              )}
            </ScrollArea>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {t("modelServices.models.selected", { count: draftModelIDs.size })}
            </span>
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                {t("common:actions.cancel")}
              </Button>
              <Button
                type="button"
                disabled={draftModelIDs.size === 0}
                onClick={confirmModels}
              >
                {t("common:actions.add")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
