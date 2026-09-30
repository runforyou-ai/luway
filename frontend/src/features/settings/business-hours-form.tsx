/** 企业客服工作时间设置表单。 */
import { useEffect, useId, useMemo, useRef } from "react"
import { PlusIcon, XIcon } from "lucide-react"
import {
  Controller,
  useFieldArray,
  useForm,
  useWatch,
  type FieldPath,
  type UseFormReturn,
} from "react-hook-form"
import { useTranslation } from "react-i18next"

import {
  getBusinessHours,
  updateBusinessHours,
  type BusinessHoursData,
} from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { SwitchCardField } from "@/components/form/switch-card-field"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import {
  clockValue,
  createBusinessHoursSchema,
  periodEndInput,
  periodEndValue,
  type BusinessHoursFormValues,
} from "@/features/settings/business-hours-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { supportedTimeZones } from "@/lib/time-zones"
import { zodResolver } from "@/lib/zod-resolver"

/** 周一到周日的文案键。 */
const weekdays = [
  "monday",
  "tuesday",
  "wednesday",
  "thursday",
  "friday",
  "saturday",
  "sunday",
] as const

/** 开启某天上班时默认填入的时段。 */
const defaultPeriod = { start: "09:00", end: "18:00" }

/** 行数变化并渲染后重新校验本组全部字段：新增的空行出现提示，删除后清除已失效的提示。 */
function useRowCountValidation(form: UseFormReturn<BusinessHoursFormValues>, names: readonly FieldPath<BusinessHoursFormValues>[]) {
  const latest = useRef(names)
  latest.current = names
  const rowCount = useRef(names.length)
  useEffect(() => {
    if (rowCount.current === names.length) return
    rowCount.current = names.length
    void form.trigger([...latest.current])
  }, [form, names.length])
}

/** 按服务端取值换算一组时段的表单值或提交值。 */
function mapPeriods(
  periods: { start: string; end: string }[],
  end: (value: string) => string,
) {
  return periods.map((period) => ({ start: clockValue(period.start), end: end(period.end) }))
}

/** 读取客服工作时间并显示设置表单。 */
export function BusinessHoursSettings() {
  const { t } = useTranslation("settings")
  const hours = useResource(resourceKeys.businessHours(), () => getBusinessHours())
  return (
    <ResourceContent resources={hours} errorMessage={t("customerService.loadError")}>
      {hours.data ? <BusinessHoursForm hours={hours.data} /> : null}
    </ResourceContent>
  )
}

/** 维护启用开关、时区、每周时段和特殊日期，修改后自动保存。 */
function BusinessHoursForm({ hours }: { hours: BusinessHoursData }) {
  const { t } = useTranslation("settings")
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () =>
      createBusinessHoursSchema({
        timeRequired: t("customerService.validation.timeRequired"),
        periodOrder: t("customerService.validation.periodOrder"),
        periodOverlap: t("customerService.validation.periodOverlap"),
        dateRequired: t("customerService.validation.dateRequired"),
        dateDuplicate: t("customerService.validation.dateDuplicate"),
      }),
    [t],
  )
  const form = useForm<BusinessHoursFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: {
      enabled: hours.enabled,
      timeZone: hours.timeZone,
      weekly: hours.weekly.map((periods) => ({ periods: mapPeriods(periods, periodEndInput) })),
      overrides: hours.overrides.map((override) => ({ date: override.date, periods: mapPeriods(override.periods, periodEndInput) })),
    },
  })
  const timeZones = useMemo(
    () => supportedTimeZones(hours.timeZone),
    [hours.timeZone],
  )
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: true,
    save: async (values) => {
      await updateBusinessHours({
        enabled: values.enabled,
        timeZone: values.timeZone,
        weekly: values.weekly.map((day) => mapPeriods(day.periods, periodEndValue)),
        overrides: values.overrides.map((override) => ({ date: override.date, periods: mapPeriods(override.periods, periodEndValue) })),
      })
      void invalidate(resourceKeys.businessHours())
    },
    errorMessage: t("customerService.saveError"),
    errorFields: ["timeZone", "weekly", "overrides"],
    logLabel: "保存客服工作时间",
  })

  return (
    <form
      className="w-full"
      aria-label={t("customerService.businessHours.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="enabled"
          control={form.control}
          render={({ field }) => (
            <SwitchCardField
              id={field.name}
              name={field.name}
              label={t("customerService.businessHours.enabled")}
              description={t("customerService.businessHours.enabledDescription")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        <Controller
          name="timeZone"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("customerService.businessHours.timeZone")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
                required
              >
                {timeZones.map((timeZone) => (
                  <option key={timeZone} value={timeZone}>
                    {timeZone}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        <WeeklyHours form={form} />
        <OverrideDates form={form} />
      </FieldGroup>
    </form>
  )
}

/** 按周一到周日逐行维护上班开关与时段。 */
function WeeklyHours({ form }: { form: UseFormReturn<BusinessHoursFormValues> }) {
  const { t } = useTranslation("settings")
  const id = useId()
  return (
    <div className="space-y-3" role="group" aria-labelledby={`${id}-label`}>
      <div>
        <div id={`${id}-label`} className="text-sm font-medium">
          {t("customerService.businessHours.weekly")}
        </div>
        <FieldDescription className="mt-1">
          {t("customerService.businessHours.weeklyDescription")}
        </FieldDescription>
      </div>
      <div className="divide-y rounded-lg border">
        {weekdays.map((weekday, index) => (
          <div className="flex items-start gap-3 px-4 py-3" key={weekday}>
            <div className="w-16 shrink-0 pt-1.5 text-sm">
              {t(`customerService.businessHours.weekdays.${weekday}`)}
            </div>
            <DayPeriods
              form={form}
              name={`weekly.${index}.periods`}
              dayLabel={t(`customerService.businessHours.weekdays.${weekday}`)}
            />
          </div>
        ))}
      </div>
    </div>
  )
}

/** 维护按日期覆盖的时段，用于节假日休息与调休上班。 */
function OverrideDates({ form }: { form: UseFormReturn<BusinessHoursFormValues> }) {
  const { control } = form
  const { t } = useTranslation("settings")
  const id = useId()
  const { fields, append, remove } = useFieldArray({
    control,
    name: "overrides",
    keyName: "fieldKey",
  })
  // 覆盖日期不能重复，任一日期变化时一并重新校验。
  const dateNames = fields.map((_, index) => `overrides.${index}.date` as const)
  useRowCountValidation(form, dateNames)
  return (
    <div className="space-y-3" role="group" aria-labelledby={`${id}-label`}>
      <div>
        <div id={`${id}-label`} className="text-sm font-medium">
          {t("customerService.businessHours.overrides")}
        </div>
        <FieldDescription className="mt-1">
          {t("customerService.businessHours.overridesDescription")}
        </FieldDescription>
      </div>
      {fields.length > 0 ? (
        <div className="divide-y rounded-lg border">
          {fields.map((item, index) => (
            <div className="flex items-start gap-3 px-4 py-3" key={item.fieldKey}>
              <Controller
                name={`overrides.${index}.date`}
                control={control}
                rules={{ deps: dateNames }}
                render={({ field, fieldState }) => (
                  <Input
                    {...field}
                    type="date"
                    className="w-40 shrink-0"
                    aria-label={t("customerService.businessHours.overrideDate", { number: index + 1 })}
                    aria-invalid={fieldState.invalid}
                    required
                  />
                )}
              />
              <DayPeriods
                form={form}
                name={`overrides.${index}.periods`}
                dayLabel={t("customerService.businessHours.overrideDate", { number: index + 1 })}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t("customerService.businessHours.removeOverride", { number: index + 1 })}
                title={t("customerService.businessHours.removeOverride", { number: index + 1 })}
                onClick={() => remove(index)}
              >
                <XIcon />
              </Button>
            </div>
          ))}
        </div>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => append({ date: "", periods: [] })}
      >
        {t("customerService.businessHours.addOverride")}
      </Button>
    </div>
  )
}

/** 一天的上班开关与时段列表；关闭上班时清空时段，开启时填入默认时段。 */
function DayPeriods({
  form,
  name,
  dayLabel,
}: {
  form: UseFormReturn<BusinessHoursFormValues>
  name: `weekly.${number}.periods` | `overrides.${number}.periods`
  dayLabel: string
}) {
  const { control } = form
  const { t } = useTranslation("settings")
  const { fields, append, remove, replace } = useFieldArray({
    control,
    name,
    keyName: "fieldKey",
  })
  const periods = useWatch({ control, name })
  const working = fields.length > 0
  // 同一天的时段顺序与重叠互相影响，任一时间变化时一并重新校验。
  const periodNames = fields.flatMap((_, index) => [`${name}.${index}.start`, `${name}.${index}.end`] as const)
  useRowCountValidation(form, periodNames)
  // 新时段从上一段结束时开始、到午夜结束；上一段已到午夜时留空由用户填写。
  const lastEnd = periods?.[periods.length - 1]?.end ?? ""
  const nextPeriod = lastEnd && lastEnd !== "00:00" ? { start: lastEnd, end: "00:00" } : { start: "", end: "" }
  return (
    <div className="flex min-w-0 flex-1 items-start gap-3">
      <div className="flex h-8 shrink-0 items-center gap-2">
        <Switch
          checked={working}
          aria-label={t("customerService.businessHours.working", { day: dayLabel })}
          onCheckedChange={(checked) => replace(checked ? [defaultPeriod] : [])}
        />
        <span className="w-8 text-sm text-muted-foreground">
          {working
            ? t("customerService.businessHours.workingShort")
            : t("customerService.businessHours.rest")}
        </span>
      </div>
      {working ? (
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {fields.map((item, index) => (
            <div className="flex items-center gap-2" key={item.fieldKey}>
              <Controller
                name={`${name}.${index}.start`}
                control={control}
                rules={{ deps: periodNames }}
                render={({ field, fieldState }) => (
                  <Input
                    {...field}
                    type="time"
                    step={60}
                    className="w-28"
                    aria-label={t("customerService.businessHours.periodStart", { day: dayLabel, number: index + 1 })}
                    aria-invalid={fieldState.invalid}
                    required
                  />
                )}
              />
              <span className="text-muted-foreground">–</span>
              <Controller
                name={`${name}.${index}.end`}
                control={control}
                rules={{ deps: periodNames }}
                render={({ field, fieldState }) => (
                  <Input
                    {...field}
                    type="time"
                    step={60}
                    className="w-28"
                    aria-label={t("customerService.businessHours.periodEnd", { day: dayLabel, number: index + 1 })}
                    aria-invalid={fieldState.invalid}
                    required
                  />
                )}
              />
              {index === 0 ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t("customerService.businessHours.addPeriod", { day: dayLabel })}
                  title={t("customerService.businessHours.addPeriod", { day: dayLabel })}
                  onClick={() => append(nextPeriod)}
                >
                  <PlusIcon />
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t("customerService.businessHours.removePeriod", { day: dayLabel, number: index + 1 })}
                  title={t("customerService.businessHours.removePeriod", { day: dayLabel, number: index + 1 })}
                  onClick={() => remove(index)}
                >
                  <XIcon />
                </Button>
              )}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}
