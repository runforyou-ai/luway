/** 移动端新建外部联系人与逐项编辑联系人资料。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useLocation, useNavigate, useParams } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  getContact,
  isNotFoundApiError,
  listChannelOptions,
  updateContact,
  type ContactDetail,
} from "@/api"
import { useMobileBack } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader, MobilePageState } from "@/apps/mobile/mobile-page"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PhoneInput } from "@/components/ui/phone-input"
import { Textarea } from "@/components/ui/textarea"
import { ContactForm } from "@/features/contacts/external/contact-form"
import { contactStageOptions } from "@/features/contacts/external/contact-labels"
import {
  contactUpdateInput,
  contactValuesFromDetail,
  useContactSchema,
} from "@/features/contacts/external/contact-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 联系人详情中可逐项编辑的资料字段及其标签。 */
export const editableContactFields = [
  { field: "displayName", label: "form.displayName" },
  { field: "stage", label: "form.stage" },
  { field: "email", label: "form.email" },
  { field: "phone", label: "form.phone" },
  { field: "notes", label: "form.notes" },
] as const

type EditableContactField = (typeof editableContactFields)[number]

/** 新建联系人，保存后替换为新联系人的详情并保留原列表的返回来源。 */
export function MobileCreateExternalContactPage() {
  const { t } = useTranslation(["contacts", "mobile"])
  const navigate = useNavigate()
  const location = useLocation()
  const close = useMobileBack("/contacts/external")
  const {
    data: channels,
    error,
    refresh,
  } = useResource(
    resourceKeys.channelOptions(),
    () => listChannelOptions(),
    { staleTime: 0 },
  )

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("detail.createTitle")}
        backTo="/contacts/external"
      />
      {error && !channels ? (
        <MobilePageState
          title={t("mobile:external.channelsLoadError")}
          onRetry={() => void refresh()}
        />
      ) : (
        <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
          <ContactForm
            channels={channels ?? []}
            onSaved={(saved) =>
              void navigate(`/contacts/external/${saved.contact.id}`, {
                replace: true,
                state: {
                  mobileBack: Boolean(
                    (location.state as { mobileBack?: boolean } | null)
                      ?.mobileBack,
                  ),
                },
              })
            }
            onCancel={close}
          />
        </div>
      )}
    </section>
  )
}

/** 读取联系人并编辑地址中指定的单项资料，拒绝未知字段。 */
export function MobileExternalContactFieldPage() {
  const { t } = useTranslation(["contacts", "mobile", "common"])
  const { contactID = "", field = "" } = useParams()
  const detailPath = `/contacts/external/${contactID}`
  const editable = editableContactFields.find((item) => item.field === field)
  // 保存时以最新资料补齐其他字段，进入页面和切回应用时重新读取。
  const {
    data: detail,
    loading,
    error,
    refresh,
  } = useResource(
    resourceKeys.contact(contactID),
    () => getContact(contactID),
    { enabled: Boolean(editable), staleTime: 0, refetchOnWindowFocus: true },
  )
  if (!editable) return <Navigate to={detailPath} replace />

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t(editable.label)}
        backTo={detailPath}
      />
      {loading && !detail ? (
        <LoadingIndicator className="min-h-64 justify-center">
          {t("common:status.loading")}
        </LoadingIndicator>
      ) : null}
      {error && !detail ? (
        <MobilePageState
          title={t(
            isNotFoundApiError(error)
              ? "mobile:external.notFound"
              : "mobile:external.loadError",
          )}
          onRetry={() => void refresh()}
        />
      ) : null}
      {detail ? (
        <MobileContactFieldForm
          key={`${detail.contact.id}:${editable.field}`}
          detail={detail}
          editable={editable}
        />
      ) : null}
    </section>
  )
}

/** 校验并保存单项资料，其他字段取最新读取的资料，保存后刷新联系人并返回详情。 */
function MobileContactFieldForm({
  detail,
  editable: { field, label },
}: {
  detail: ContactDetail
  editable: EditableContactField
}) {
  const { t } = useTranslation(["contacts", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const close = useMobileBack(`/contacts/external/${detail.contact.id}`)
  const contactSchema = useContactSchema()
  const current = contactValuesFromDetail(detail)
  const schema = z.object({ value: contactSchema.shape[field] })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { value: current[field] },
  })
  const { mounted, dirty } = useFormLifetime(form.formState.isDirty)
  const stage = form.watch("value")

  /** 当前项有变化时与最新资料的其他字段一起保存，离开编辑页后忽略迟到的结果。 */
  async function save({ value }: z.infer<typeof schema>) {
    if (value !== current[field]) {
      try {
        await updateContact(
          detail.contact.id,
          contactUpdateInput(detail, { ...current, [field]: value }),
        )
        void invalidate(resourceKeys.contact(detail.contact.id))
        void invalidate(resourceKeys.contacts())
      } catch (error) {
        if (!mounted.current || recoverSession(error, navigate)) return
        console.warn("移动端保存联系人失败", error)
        toast.error(
          requestErrorMessage(error, ["displayName", "stage", "methods", "notes"]),
        )
        return
      }
    }
    if (!mounted.current) return
    form.reset({ value })
    dirty.current = false
    close()
  }

  const saving = form.formState.isSubmitting
  return (
    <form
      className="app-form min-h-0 flex-1 space-y-9 overflow-y-auto overscroll-contain p-4"
      noValidate
      onSubmit={form.handleSubmit(save)}
    >
      {field === "stage" ? (
        <div
          role="group"
          aria-label={t("form.stage")}
          className="grid gap-2"
        >
          {contactStageOptions.map((item) => (
            <Button
              key={item.value}
              type="button"
              variant={stage === item.value ? "default" : "outline"}
              className="min-h-11"
              aria-pressed={stage === item.value}
              disabled={saving}
              onClick={() =>
                form.setValue("value", item.value, { shouldDirty: true })
              }
            >
              {t(item.label)}
            </Button>
          ))}
        </div>
      ) : (
        <Field>
          <FieldLabel htmlFor="mobile-contact-field">
            {t(label)}
          </FieldLabel>
          {field === "phone" ? (
            <Controller
              name="value"
              control={form.control}
              render={({ field: input, fieldState }) => (
                <PhoneInput
                  ref={input.ref}
                  id="mobile-contact-field"
                  name={input.name}
                  value={input.value}
                  onChange={input.onChange}
                  onBlur={input.onBlur}
                  aria-invalid={fieldState.invalid}
                  autoComplete="tel"
                  disabled={saving}
                />
              )}
            />
          ) : field === "notes" ? (
            <Textarea
              {...form.register("value")}
              id="mobile-contact-field"
              rows={6}
              disabled={saving}
              className="md:text-base"
            />
          ) : (
            <Input
              {...form.register("value")}
              id="mobile-contact-field"
              type={field === "email" ? "email" : "text"}
              autoComplete={field === "email" ? "email" : "name"}
              disabled={saving}
              className="min-h-11 md:text-base"
            />
          )}
        </Field>
      )}
      <Button type="submit" className="min-h-11 w-full" disabled={saving}>
        {saving ? t("common:actions.saving") : t("common:actions.save")}
      </Button>
    </form>
  )
}
