/** 服务器协议选择与地址输入。 */
import { LoaderCircleIcon, SearchIcon } from "lucide-react"
import { Controller, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { splitServerURL, type ServerConnectionFormValues } from "@/features/server-connection/server-connection-schema"

/** 在同一行选择 HTTP/HTTPS 并输入服务器地址，粘贴完整地址时同步拆分协议。 */
export function ServerAddressField({ form, busy, detecting, onDetect }: {
  form: UseFormReturn<ServerConnectionFormValues>
  busy: boolean
  detecting: boolean
  onDetect: () => void
}) {
  const { t } = useTranslation("connection")
  return (
    <Controller
      name="address"
      control={form.control}
      render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor="server-address" required>{t("serverUrlLabel")}</FieldLabel>
          <div className="flex">
            <Controller
              name="protocol"
              control={form.control}
              render={({ field: protocol }) => (
                <NativeSelect
                  {...protocol}
                  aria-label={t("serverProtocolLabel")}
                  className="w-28 rounded-r-none border-r-0 shadow-none"
                  disabled={busy}
                >
                  <option value="https://">https://</option>
                  <option value="http://">http://</option>
                </NativeSelect>
              )}
            />
            <div className="relative min-w-0 flex-1">
              <Input
                {...field}
                id="server-address"
                type="text"
                inputMode="url"
                autoCapitalize="none"
                autoCorrect="off"
                autoComplete="off"
                spellCheck={false}
                autoFocus
                required
                disabled={busy}
                aria-invalid={fieldState.invalid}
                className="rounded-l-none pr-10"
                onChange={(event) => {
                  const value = event.target.value
                  if (/^https?:\/\//i.test(value.trim())) {
                    const parsed = splitServerURL(value)
                    form.setValue("protocol", parsed.protocol, { shouldDirty: true })
                    field.onChange(parsed.address)
                  } else {
                    field.onChange(value)
                  }
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
                disabled={busy}
                aria-label={detecting ? t("detecting") : t("detect")}
                onClick={onDetect}
              >
                {detecting ? <LoaderCircleIcon className="animate-spin" /> : <SearchIcon />}
              </Button>
            </div>
          </div>
        </Field>
      )}
    />
  )
}
