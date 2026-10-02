/** 创建工作区页：填写名称和标识，创建后进入新工作区。 */
import { useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"
import { toast } from "sonner"

import { createWorkspace, isApiError } from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import {
  createWorkspaceSchema,
  randomWorkspaceSlug,
  suggestWorkspaceSlug,
  type WorkspaceFormValues,
} from "@/lib/workspace-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { resolveServerURL } from "@/lib/server-url"
import { recoverSession } from "@/lib/session-navigation"
import { enterWorkspace, navigateToHashPath, returnToPath } from "@/lib/workspace-route"
import { zodResolver } from "@/lib/zod-resolver"

/** 校验并创建工作区，标识未手动修改时按名称自动建议；带返回地址直接进入时返回原工作区页面，否则返回工作区选择页。 */
export function WorkspaceCreatePage() {
  const { t } = useTranslation(["account", "common"])
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  // 从工作区内直接进入时返回原工作区页面；经工作区列表进入时返回列表并保留列表的返回地址。
  const returnTo = returnToPath(searchParams)
  const viaList = searchParams.get("via") === "list"
  const serverURL = useResource(resourceKeys.serverURL(), () => resolveServerURL())
  const host = serverURL.data ? new URL(serverURL.data).host : ""
  const schema = useMemo(
    () =>
      createWorkspaceSchema({
        nameRequired: t("nameRequired"),
        nameTooLong: t("nameTooLong"),
        slugRequired: t("slugRequired"),
        slugInvalid: t("slugInvalid"),
      }),
    [t],
  )
  const form = useForm<WorkspaceFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { name: "", slug: "" },
  })

  // 工作区标识未手动修改时按名称自动建议，名称无法转成标识时使用本页生成的随机标识。
  const [fallbackSlug] = useState(randomWorkspaceSlug)
  const { getFieldState, setValue, watch } = form
  const name = watch("name")
  const slug = watch("slug").trim().toLowerCase()
  useEffect(() => {
    if (!getFieldState("slug").isDirty) {
      setValue("slug", suggestWorkspaceSlug(name, fallbackSlug))
    }
  }, [fallbackSlug, getFieldState, name, setValue])

  /** 提交新建工作区并进入。 */
  async function submitWorkspace(values: WorkspaceFormValues) {
    try {
      const workspace = await createWorkspace(values)
      enterWorkspace(workspace.slug)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      toast.error(isApiError(error) ? apiErrorMessage(error, ["name", "slug"]) : t("createError"))
    }
  }

  const { isSubmitting } = form.formState

  return (
    <EntryLayout
      title={t("createTitle")}
      description={t("createDescription")}
      leading={
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="mb-3 -ml-2 text-muted-foreground"
          aria-label={t("common:actions.back")}
          title={t("common:actions.back")}
          onClick={() => {
            if (returnTo && !viaList) {
              navigateToHashPath(returnTo)
              return
            }
            navigate(returnTo ? `/workspaces?returnTo=${encodeURIComponent(returnTo)}` : "/workspaces")
          }}
        >
          <ArrowLeftIcon />
        </Button>
      }
    >
      <form className="space-y-9" onSubmit={form.handleSubmit(submitWorkspace)} noValidate>
        <FieldGroup>
          <FormInputField name="name" control={form.control} label={t("nameLabel")} autoFocus />
          <div className="space-y-2">
            <FormInputField name="slug" control={form.control} label={t("slugLabel")} autoCapitalize="none" autoCorrect="off" />
            <p className="truncate text-xs text-muted-foreground">
              {t("addressPreview", { address: `${host}/#/w/${slug || "…"}` })}
            </p>
          </div>
        </FieldGroup>
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("creating") : t("create")}
        </Button>
      </form>
    </EntryLayout>
  )
}
