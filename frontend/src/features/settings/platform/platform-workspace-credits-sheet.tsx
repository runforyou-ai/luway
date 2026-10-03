/** 平台管理员查看工作区积分的侧栏：余额、积分流水与手动调整积分的弹窗。 */
import { useMemo, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  adjustPlatformWorkspaceCredits,
  getPlatformWorkspaceCredits,
  listPlatformWorkspaceCreditEntries,
  type PlatformWorkspace,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { WorkspaceAddress } from "@/components/workspace-address"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { CreditBalanceTiles, CreditEntryTable } from "@/features/settings/credit-ledger"
import { idleRefreshInterval, isRunningCall, runningRefreshInterval } from "@/features/settings/credits-page"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCreditFormat } from "@/hooks/use-credit-format"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { usePagedResource, useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 单次调整积分的上限。 */
const maxCreditAdjustment = 1_000_000_000_000

/** 按工作区打开侧栏，workspace 为空时关闭；展示余额与积分流水并定时刷新，可手动增加或扣减积分。 */
export function PlatformWorkspaceCreditsSheet({
  workspace,
  onClose,
}: {
  workspace: Pick<PlatformWorkspace, "id" | "name" | "slug"> | null
  onClose: () => void
}) {
  const { t } = useTranslation("platform")
  const workspaceId = workspace?.id ?? ""
  const [adjusting, setAdjusting] = useState(false)
  const entries = usePagedResource(
    resourceKeys.platformWorkspaceCreditEntries(workspaceId, { pageSize: 50 }),
    (page, signal) => listPlatformWorkspaceCreditEntries(workspaceId, { page, pageSize: 50 }, signal),
    {
      select: (data) => ({ items: data.entries, page: data.page }),
      itemKey: (entry) => `${entry.kind}:${entry.id}`,
      enabled: Boolean(workspaceId),
      staleTime: 0,
      refetchInterval: (items) => (items.some(isRunningCall) ? runningRefreshInterval : idleRefreshInterval),
    },
  )
  const running = entries.data?.items.some(isRunningCall) ?? false
  const balance = useResource(
    resourceKeys.platformWorkspaceCredits(workspaceId),
    (signal) => getPlatformWorkspaceCredits(workspaceId, signal),
    { enabled: Boolean(workspaceId), staleTime: 0, refetchInterval: running ? runningRefreshInterval : idleRefreshInterval },
  )

  return (
    <Sheet open={Boolean(workspace)} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent className="w-full gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle>{workspace?.name}</SheetTitle>
          <SheetDescription>
            {workspace ? <WorkspaceAddress slug={workspace.slug} className="mb-1" /> : null}
            {t("workspaceCredits.description")}
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-6">
            <ResourceContent resources={[balance, entries]} errorMessage={t("workspaceCredits.loadError")}>
              <div className="space-y-6">
                {balance.data ? <CreditBalanceTiles balance={balance.data} /> : null}
                <div>
                  <Button type="button" variant="outline" onClick={() => setAdjusting(true)}>
                    {t("workspaceCredits.adjust")}
                  </Button>
                </div>
                <ResourceListFrame more={entries.more}>
                  <CreditEntryTable entries={entries.data?.items ?? []} />
                </ResourceListFrame>
              </div>
            </ResourceContent>
          </div>
        </ScrollArea>
      </SheetContent>
      {workspace ? (
        <AdjustCreditsDialog workspace={workspace} open={adjusting} onOpenChange={setAdjusting} />
      ) : null}
    </Sheet>
  )
}

/** 调整积分弹窗：选择增加或扣减，填写积分与备注；扣减超过余额时扣到 0 并提示实际扣减的积分。 */
function AdjustCreditsDialog({
  workspace,
  open,
  onOpenChange,
}: {
  workspace: Pick<PlatformWorkspace, "id" | "name" | "slug">
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation("platform")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("workspaceCredits.adjustTitle", { name: workspace.name })}</DialogTitle>
          <DialogDescription>
            <WorkspaceAddress slug={workspace.slug} className="mb-1" />
            {t("workspaceCredits.adjustDescription")}
          </DialogDescription>
        </DialogHeader>
        {open ? <AdjustCreditsForm workspaceId={workspace.id} onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  )
}

/** 调整积分表单，提交成功后刷新余额与流水并关闭弹窗。 */
function AdjustCreditsForm({ workspaceId, onDone }: { workspaceId: string; onDone: () => void }) {
  const { t } = useTranslation(["platform", "common"])
  const navigate = useNavigate()
  const mounted = useMountedRef()
  const invalidate = useResourceInvalidator()
  const credit = useCreditFormat()
  const schema = useMemo(
    () =>
      z.object({
        direction: z.enum(["add", "deduct"]),
        amount: z
          .string()
          .trim()
          .refine((value) => /^\d+$/.test(value) && Number(value) > 0 && Number(value) <= maxCreditAdjustment, t("workspaceCredits.amountInvalid")),
        note: z.string().trim().min(1, t("workspaceCredits.noteRequired")).max(500, t("workspaceCredits.noteTooLong")),
      }),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { direction: "add", amount: "", note: "" },
  })

  /** 提交调整，按实际变动积分提示结果。 */
  async function submit(values: z.infer<typeof schema>) {
    const requested = Number(values.amount.trim())
    try {
      const result = await adjustPlatformWorkspaceCredits(workspaceId, {
        amount: values.direction === "add" ? requested : -requested,
        note: values.note.trim(),
      })
      void invalidate(resourceKeys.platformWorkspaceCredits(workspaceId))
      void invalidate(resourceKeys.platformWorkspaceCreditEntries(workspaceId))
      const amount = credit.amount(Math.abs(result.amount))
      toast.success(
        result.amount > 0
          ? t("workspaceCredits.added", { amount })
          : Math.abs(result.amount) < requested
            ? t("workspaceCredits.deductedPartially", { amount })
            : t("workspaceCredits.deducted", { amount }),
      )
      if (mounted.current) onDone()
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      console.warn("调整工作区积分失败", error)
      toast.error(requestErrorMessage(error, ["amount", "note"]))
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <div className="grid gap-4 sm:grid-cols-[auto_1fr]">
          <Controller
            name="direction"
            control={form.control}
            render={({ field }) => (
              <Field>
                <FieldLabel htmlFor="credit-adjust-direction" required>
                  {t("workspaceCredits.direction")}
                </FieldLabel>
                <NativeSelect {...field} id="credit-adjust-direction" required>
                  <option value="add">{t("workspaceCredits.directions.add")}</option>
                  <option value="deduct">{t("workspaceCredits.directions.deduct")}</option>
                </NativeSelect>
              </Field>
            )}
          />
          <FormInputField
            name="amount"
            id="credit-adjust-amount"
            control={form.control}
            label={t("workspaceCredits.amount")}
            inputMode="numeric"
            autoComplete="off"
            autoFocus
          />
        </div>
        <FormInputField name="note" id="credit-adjust-note" control={form.control} label={t("workspaceCredits.note")} maxLength={500} autoComplete="off" />
      </FieldGroup>
      <div className="flex items-center justify-end gap-2">
        <Button type="button" variant="outline" disabled={isSubmitting} onClick={onDone}>
          {t("common:actions.cancel")}
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("workspaceCredits.adjusting") : t("common:actions.confirm")}
        </Button>
      </div>
    </form>
  )
}
