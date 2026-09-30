/** 模型供应商品牌、名称与连接凭据字段。 */
import { Controller, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { AIProviderCredentialType, type AIProviderBrandId } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { ModelProviderBrandIcon } from "./model-provider-brand-icon"
import { aiProviderBrandConfigs } from "./model-provider-brands"
import type { AIProviderFormValues } from "./model-provider-schema"

/** 展示供应商连接字段及条件必填的密钥。 */
export function ModelProviderConnectionFields({ form, mode }: { form: UseFormReturn<AIProviderFormValues>; mode: "create" | "edit" }) {
  const { t } = useTranslation("integrations")
  const watchedBrand = form.watch("brand") as AIProviderBrandId
  const brandConfig = aiProviderBrandConfigs[watchedBrand]
  const brandName = t(brandConfig.nameKey)
  const usesAPIKey =
    form.watch("credentialType") ===
    AIProviderCredentialType.AIProviderCredentialTypeAPIKey

  return (
    <FieldGroup>
      <Field>
        <FieldLabel>{t("modelServices.form.brand")}</FieldLabel>
        <div className="flex h-9 items-center gap-2 text-sm">
          <ModelProviderBrandIcon brand={watchedBrand} className="size-7 rounded-md [&>svg]:size-4" />
          {brandName}
        </div>
      </Field>
      <FormInputField
        name="name"
        control={form.control}
        label={t("modelServices.form.name")}
        autoFocus={mode === "create"}
      />
      {brandConfig.supportsNoCredential ? (
        <Controller
          name="credentialType"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="model-provider-credential-type" required>
                {t("modelServices.form.credentialType")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id="model-provider-credential-type"
                required
                aria-invalid={fieldState.invalid}
                onChange={(event) => {
                  const next = event.target
                    .value as AIProviderFormValues["credentialType"]
                  field.onChange(next)
                  // 不需要凭据的服务不保留已填写的密钥。
                  if (
                    next ===
                    AIProviderCredentialType.AIProviderCredentialTypeNone
                  ) {
                    form.setValue("apiKey", "", { shouldDirty: true })
                  }
                }}
              >
                <option
                  value={
                    AIProviderCredentialType.AIProviderCredentialTypeAPIKey
                  }
                >
                  {t("modelServices.form.credentialTypes.apiKey")}
                </option>
                <option
                  value={AIProviderCredentialType.AIProviderCredentialTypeNone}
                >
                  {t("modelServices.form.credentialTypes.none")}
                </option>
              </NativeSelect>
            </Field>
          )}
        />
      ) : null}
      {usesAPIKey ? (
        <FormInputField
          name="apiKey"
          control={form.control}
          label={t("credentials.apiKey")}
          autoComplete="off"
          passwordVisibilityLabels={{
            show: t("credentials.showAPIKey"),
            hide: t("credentials.hideAPIKey"),
          }}
        />
      ) : null}
      <FormInputField
        name="apiUrl"
        control={form.control}
        label={t("modelServices.form.apiUrl")}
        inputMode="url"
      />
    </FieldGroup>
  )
}
