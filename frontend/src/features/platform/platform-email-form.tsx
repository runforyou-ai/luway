/** 系统邮件的接入方式、发件人与服务商配置表单。 */
import { useMemo, useRef, useState } from "react"
import { Controller, useForm, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { EmailProvider, updatePlatformEmail, type PlatformEmailSettings } from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { useFormSave } from "@/hooks/use-form-save"
import { zodResolver } from "@/lib/zod-resolver"
import { EmailTestDialog } from "./platform-email-test-dialog"
import { createEmailSchema, emailFormValues, emailSecretStored, type EmailFormValues } from "./platform-email-schema"

/** 保存一种接入方式，保存成功后清除密钥草稿。 */
export function EmailSettingsForm({ email, onSaved }: { email: PlatformEmailSettings; onSaved: () => void }) {
  const { t } = useTranslation("platform")
  const [saved, setSaved] = useState(email)
  const [testOpen, setTestOpen] = useState(false)
  const testButton = useRef<HTMLButtonElement>(null)
  const schema = useMemo(() => createEmailSchema(t, saved), [t, saved])
  const form = useForm<EmailFormValues>({ resolver: zodResolver(schema), shouldUseNativeValidation: true, defaultValues: emailFormValues(email) })
  const values = form.watch()
  const { submit } = useFormSave({
    form, schema, autoSave: false,
    save: v => updatePlatformEmail({
      enabled: v.enabled, provider: v.provider, fromAddress: v.fromAddress, fromName: v.fromName,
      smtp: v.provider === EmailProvider.SMTP ? { ...v.smtp, port: Number(v.smtp.port) } : null,
      ses: v.provider === EmailProvider.SES ? v.ses : null,
      mailgun: v.provider === EmailProvider.Mailgun ? v.mailgun : null,
      postmark: v.provider === EmailProvider.Postmark ? v.postmark : null,
      resend: v.provider === EmailProvider.Resend ? v.resend : null,
    }),
    onSubmitted: result => {
      setSaved(result.email)
      form.reset(emailFormValues(result.email))
      toast.success(t("deployment.saved"))
      onSaved()
    },
    errorMessage: t("deployment.saveError"), logLabel: "保存邮件发送配置",
    errorFields: ["provider", "fromAddress", "fromName", "smtp.host", "smtp.port", "smtp.security", "smtp.username", "smtp.password", "ses.region", "ses.accessKeyID", "ses.secretAccessKey", "mailgun.domain", "mailgun.region", "mailgun.apiKey", "postmark.serverToken", "postmark.messageStream", "resend.apiKey"],
  })
  return <div className="space-y-9">
    <form className="space-y-9" aria-label={t("deployment.tabs.email")} onSubmit={form.handleSubmit(submit)} noValidate>
      <fieldset disabled={form.formState.isSubmitting}>
        <FieldGroup>
          <Controller name="enabled" control={form.control} render={({ field }) => <SwitchField {...field} id="email-enabled" label={t("deployment.email.enabled")} description={t("deployment.email.enabledHelp")} checked={field.value} onCheckedChange={field.onChange} />} />
          <Controller name="provider" control={form.control} render={({ field, fieldState }) => <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor="email-provider" required>{t("deployment.email.provider")}</FieldLabel>
            <NativeSelect {...field} id="email-provider" aria-invalid={fieldState.invalid}>
              <option value={EmailProvider.SMTP}>SMTP</option>
              <option value={EmailProvider.SES}>Amazon SES</option>
              <option value={EmailProvider.Mailgun}>Mailgun</option>
              <option value={EmailProvider.Postmark}>Postmark</option>
              <option value={EmailProvider.Resend}>Resend</option>
            </NativeSelect>
          </Field>} />
          <div className="grid gap-6 sm:grid-cols-2">
            <FormInputField name="fromAddress" control={form.control} label={t("deployment.email.fromAddress")} type="email" required={values.enabled} />
            <FormInputField name="fromName" control={form.control} label={t("deployment.email.fromName")} description={t("deployment.email.fromNameHelp")} required={false} />
          </div>
          <EmailProviderFields form={form} saved={saved} />
        </FieldGroup>
      </fieldset>
      <FormActions saving={form.formState.isSubmitting}>
        <span title={t("deployment.email.testHelp")}>
          <Button ref={testButton} type="button" variant="outline" disabled={!saved.enabled || form.formState.isDirty || form.formState.isSubmitting} onClick={() => setTestOpen(true)}>{t("deployment.email.test")}</Button>
        </span>
      </FormActions>
    </form>
    <EmailTestDialog open={testOpen} onOpenChange={setTestOpen} triggerRef={testButton} />
  </div>
}

/** 按当前接入方式显示专属字段和已保存密钥的状态。 */
function EmailProviderFields({ form, saved }: { form: UseFormReturn<EmailFormValues>; saved: PlatformEmailSettings }) {
  const { t } = useTranslation("platform")
  const values = form.watch()
  const stored = emailSecretStored(values, saved)
  const secret = {
    control: form.control, autoComplete: "new-password", required: values.enabled && !stored,
    description: stored ? t("deployment.email.secretStored") : undefined,
    passwordVisibilityLabels: { show: t("deployment.showSecret"), hide: t("deployment.hideSecret") },
  }
  switch (values.provider) {
    case EmailProvider.SMTP: return <>
      <FormInputField name="smtp.host" control={form.control} label={t("deployment.email.host")} required={values.enabled} deps={["smtp.password"]} />
      <div className="grid gap-6 sm:grid-cols-2">
        <FormInputField name="smtp.port" control={form.control} label={t("deployment.email.port")} inputMode="numeric" />
        <Controller name="smtp.security" control={form.control} render={({ field, fieldState }) => <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor="smtp-security" required>{t("deployment.email.security")}</FieldLabel>
          <NativeSelect {...field} id="smtp-security" aria-invalid={fieldState.invalid}>
            <option value="starttls">STARTTLS</option><option value="tls">SSL/TLS</option><option value="none">{t("deployment.email.securityNone")}</option>
          </NativeSelect>
        </Field>} />
        <FormInputField name="smtp.username" control={form.control} label={t("deployment.email.username")} description={t("deployment.email.usernameHelp")} autoComplete="off" required={false} deps={["smtp.password"]} />
        <FormInputField {...secret} name="smtp.password" label={t("deployment.email.password")} required={values.enabled && !!values.smtp.username && !stored} />
      </div>
    </>
    case EmailProvider.SES: return <>
      <FormInputField name="ses.region" control={form.control} label={t("deployment.email.sesRegion")} description={t("deployment.email.sesRegionHelp")} required={values.enabled} />
      <FormInputField name="ses.accessKeyID" control={form.control} label="Access Key ID" autoComplete="off" required={values.enabled} deps={["ses.secretAccessKey"]} />
      <FormInputField {...secret} name="ses.secretAccessKey" label="Secret Access Key" />
    </>
    case EmailProvider.Mailgun: return <>
      <div className="grid gap-6 sm:grid-cols-2">
        <FormInputField name="mailgun.domain" control={form.control} label={t("deployment.email.domain")} required={values.enabled} />
        <Controller name="mailgun.region" control={form.control} render={({ field, fieldState }) => <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor="mailgun-region" required>{t("deployment.email.mailgunRegion")}</FieldLabel>
          <NativeSelect {...field} id="mailgun-region" aria-invalid={fieldState.invalid}><option value="us">US</option><option value="eu">EU</option></NativeSelect>
        </Field>} />
      </div>
      <FormInputField {...secret} name="mailgun.apiKey" label="API Key" />
    </>
    case EmailProvider.Postmark: return <>
      <FormInputField {...secret} name="postmark.serverToken" label="Server Token" />
      <FormInputField name="postmark.messageStream" control={form.control} label={t("deployment.email.messageStream")} description={t("deployment.email.messageStreamHelp")} required={false} />
    </>
    case EmailProvider.Resend: return <FormInputField {...secret} name="resend.apiKey" label="API Key" />
  }
}
