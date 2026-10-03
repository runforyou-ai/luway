/** 平台设置的商业服务页：未配对时粘贴配对码完成配对；已配对时展示商业服务与同步状态，可立即同步、重新配对或解除配对。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import { CommerceSyncFailure, getCommercePairing, isApiError, pairCommerce, syncCommerce, unpairCommerce, type CommercePairing } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 配对后等待首次同步完成时刷新配对状态的间隔毫秒数。 */
const pendingRefreshInterval = 3000

/** 常规刷新配对状态的间隔毫秒数。 */
const idleRefreshInterval = 30000

/** 每次进入页面读取最新配对状态并定时刷新后台同步结果，渲染配对表单或配对详情。 */
export function PlatformCommercePage() {
  const { t } = useTranslation("platform")
  // 配对后首次同步完成前短间隔刷新，之后按常规间隔刷新后台同步结果。
  const pairing = useResource(resourceKeys.commercePairing(), (signal) => getCommercePairing(signal), {
    staleTime: 0,
    refetchInterval: (data) => (data?.paired && !data.syncedAt && !data.failedAt ? pendingRefreshInterval : idleRefreshInterval),
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("commerce.title")} description={t("commerce.description")} />
      <PageContent variant="form">
        <ResourceContent resources={pairing} errorMessage={t("commerce.loadError")}>
          {pairing.data ? (
            pairing.data.paired ? <PairingDetails pairing={pairing.data} /> : <PairingForm />
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 只读展示商业服务地址、标识、配对时间与同步状态，提供立即同步、重新配对和解除配对。 */
function PairingDetails({ pairing }: { pairing: CommercePairing }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [syncing, setSyncing] = useState(false)
  const [repairing, setRepairing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [unpairing, setUnpairing] = useState(false)
  const [unpairPending, setUnpairPending] = useState(false)
  const repairButtonRef = useRef<HTMLButtonElement>(null)
  const unpairButtonRef = useRef<HTMLButtonElement>(null)
  const busy = syncing || unpairPending
  const failureReasons: Record<string, string> = {
    [CommerceSyncFailure.CommerceSyncFailureUnavailable]: t("commerce.failures.unavailable"),
    [CommerceSyncFailure.CommerceSyncFailureNotPaired]: t("commerce.failures.not_paired"),
    [CommerceSyncFailure.CommerceSyncFailureInvalidData]: t("commerce.failures.invalid_data"),
  }
  const failureReason = (pairing.failure && failureReasons[pairing.failure]) || t("commerce.failures.failed")

  /** 立即读取商业服务的变更。 */
  async function sync() {
    setSyncing(true)
    try {
      await syncCommerce()
      toast.success(t("commerce.synced"))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("同步商业服务失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, []) : t("commerce.syncError"))
    } finally {
      setSyncing(false)
      void invalidate(resourceKeys.commercePairing())
    }
  }

  /** 解除配对后回到配对表单。 */
  async function unpair() {
    setUnpairPending(true)
    try {
      await unpairCommerce()
      setUnpairing(false)
      toast.success(t("commerce.unpaired"))
      void invalidate(resourceKeys.commercePairing())
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("解除商业服务配对失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, []) : t("commerce.unpairError"))
    } finally {
      setUnpairPending(false)
    }
  }

  return (
    <div className="w-full space-y-9">
      <FieldGroup>
        <div className="grid gap-6 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="commerce-url">{t("commerce.url")}</FieldLabel>
            <Input id="commerce-url" value={pairing.url} readOnly className="text-muted-foreground" />
          </Field>
          <Field>
            <FieldLabel htmlFor="commerce-service-id">{t("commerce.serviceId")}</FieldLabel>
            <Input id="commerce-service-id" value={pairing.serviceId} readOnly className="font-mono text-muted-foreground" />
          </Field>
        </div>
        <div className="grid gap-6 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="commerce-paired-at">{t("commerce.pairedAt")}</FieldLabel>
            <Input
              id="commerce-paired-at"
              value={pairing.pairedAt ? formatDateTime(pairing.pairedAt) : ""}
              readOnly
              className="text-muted-foreground"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="commerce-synced-at">{t("commerce.syncedAt")}</FieldLabel>
            <Input
              id="commerce-synced-at"
              value={pairing.syncedAt ? formatDateTime(pairing.syncedAt) : t("commerce.neverSynced")}
              readOnly
              className="text-muted-foreground"
            />
            {pairing.failedAt ? (
              <FieldDescription className="text-destructive">
                {t("commerce.syncFailed", { time: formatDateTime(pairing.failedAt), reason: failureReason })}
              </FieldDescription>
            ) : null}
          </Field>
        </div>
      </FieldGroup>
      <div className="flex flex-wrap justify-end gap-2">
        <Button
          ref={unpairButtonRef}
          type="button"
          variant="outline"
          className="touch:min-h-11 touch:flex-1"
          disabled={busy}
          onClick={() => setUnpairing(true)}
        >
          {t("commerce.unpair")}
        </Button>
        <Button
          ref={repairButtonRef}
          type="button"
          variant="outline"
          className="touch:min-h-11 touch:flex-1"
          disabled={busy}
          onClick={() => setRepairing(true)}
        >
          {t("commerce.repair")}
        </Button>
        <Button type="button" className="touch:min-h-11 touch:flex-1" disabled={busy} onClick={() => void sync()}>
          {syncing ? <LoaderCircleIcon className="animate-spin" /> : null}
          {syncing ? t("commerce.syncing") : t("commerce.sync")}
        </Button>
      </div>
      <Dialog open={repairing} onOpenChange={(open) => !submitting && setRepairing(open)}>
        <DialogContent
          closeDisabled={submitting}
          onCloseAutoFocus={(event) => {
            // 关闭弹窗后焦点回到重新配对按钮。
            event.preventDefault()
            repairButtonRef.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("commerce.repairTitle")}</DialogTitle>
            <DialogDescription>{t("commerce.repairDescription")}</DialogDescription>
          </DialogHeader>
          {repairing ? (
            <PairingForm onCancel={() => setRepairing(false)} onPaired={() => setRepairing(false)} onSubmittingChange={setSubmitting} />
          ) : null}
        </DialogContent>
      </Dialog>
      <ConfirmationDialog
        open={unpairing}
        pending={unpairPending}
        title={t("commerce.unpairTitle")}
        description={t("commerce.unpairDescription")}
        onOpenChange={setUnpairing}
        onConfirm={() => void unpair()}
        onCloseAutoFocus={(event) => {
          // 关闭确认框后焦点回到解除配对按钮。
          event.preventDefault()
          unpairButtonRef.current?.focus()
        }}
      />
    </div>
  )
}

/** 粘贴配对码完成配对或替换现有配对；配对成功后刷新配对状态。 */
function PairingForm({
  onCancel,
  onPaired,
  onSubmittingChange,
}: {
  onCancel?: () => void
  onPaired?: () => void
  onSubmittingChange?: (submitting: boolean) => void
}) {
  const { t } = useTranslation(["platform", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () => z.object({ pairingCode: z.string().trim().min(1, t("commerce.pairingCodeRequired")) }),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { pairingCode: "" },
  })
  useFormLifetime(form.formState.isDirty)

  /** 提交配对码。 */
  async function pair(values: z.infer<typeof schema>) {
    try {
      await pairCommerce({ pairingCode: values.pairingCode })
      form.reset()
      toast.success(t("commerce.paired"))
      void invalidate(resourceKeys.commercePairing())
      onPaired?.()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("配对商业服务失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, ["pairingCode"]) : t("commerce.pairError"))
    }
  }

  const { isSubmitting } = form.formState

  // 向弹窗同步提交状态，提交期间停用关闭入口；表单卸载时恢复。
  useEffect(() => {
    onSubmittingChange?.(isSubmitting)
    return () => onSubmittingChange?.(false)
  }, [isSubmitting, onSubmittingChange])

  return (
    <form className="w-full space-y-9" aria-label={t("commerce.pair")} onSubmit={form.handleSubmit(pair)} noValidate>
      <FieldGroup>
        <Controller
          name="pairingCode"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("commerce.pairingCode")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                rows={5}
                spellCheck={false}
                autoComplete="off"
                className="font-mono text-xs break-all"
                aria-invalid={fieldState.invalid}
                required
              />
              <FieldDescription>{t("commerce.pairingCodeHelp")}</FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <div className="flex justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="outline" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting} onClick={onCancel}>
            {t("common:actions.cancel")}
          </Button>
        ) : null}
        <Button type="submit" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("commerce.pairing") : t("commerce.pair")}
        </Button>
      </div>
    </form>
  )
}
