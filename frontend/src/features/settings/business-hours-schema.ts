/** 客服工作时间表单校验规则与时段取值换算。 */
import { z } from "zod"

/** 把时间输入框的值规整为 HH:mm。 */
export function clockValue(value: string) {
  return value.slice(0, 5)
}

/** 把表单中的结束时间换算为服务端取值：00:00 表示当天午夜，即 24:00。 */
export function periodEndValue(value: string) {
  const clock = clockValue(value)
  return clock === "00:00" ? "24:00" : clock
}

/** 把服务端结束时间换算为时间输入框可显示的值：24:00 显示为 00:00。 */
export function periodEndInput(value: string) {
  return value === "24:00" ? "00:00" : value
}

/** 创建客服工作时间校验：时段起止必填且结束晚于开始（结束 00:00 表示午夜），同一天时段不重叠，日期覆盖必须选择日期且不重复。 */
export function createBusinessHoursSchema(messages: {
  timeRequired: string
  periodOrder: string
  periodOverlap: string
  dateRequired: string
  dateDuplicate: string
}) {
  const period = z.object({
    start: z.string().min(1, messages.timeRequired),
    end: z.string().min(1, messages.timeRequired),
  })
  // 同一天的时段按开始时间比较，结束早于或等于开始、与前一段重叠时标记在对应的结束或开始时间上。
  const periods = z.array(period).superRefine((items, context) => {
    items.forEach((item, index) => {
      if (item.start && item.end && periodEndValue(item.end) <= clockValue(item.start)) {
        context.addIssue({ code: "custom", path: [index, "end"], message: messages.periodOrder })
      }
    })
    const ordered = items
      .map((item, index) => ({ start: clockValue(item.start), end: periodEndValue(item.end), index }))
      .sort((left, right) => left.start.localeCompare(right.start))
    for (let position = 1; position < ordered.length; position++) {
      if (ordered[position].start < ordered[position - 1].end) {
        context.addIssue({ code: "custom", path: [ordered[position].index, "start"], message: messages.periodOverlap })
      }
    }
  })
  return z.object({
    enabled: z.boolean(),
    timeZone: z.string().min(1),
    weekly: z.array(z.object({ periods })).length(7),
    overrides: z
      .array(z.object({ date: z.string().min(1, messages.dateRequired), periods }))
      .superRefine((items, context) => {
        const seen = new Set<string>()
        items.forEach((item, index) => {
          if (item.date && seen.has(item.date)) {
            context.addIssue({ code: "custom", path: [index, "date"], message: messages.dateDuplicate })
          }
          seen.add(item.date)
        })
      }),
  })
}

export type BusinessHoursFormValues = z.infer<
  ReturnType<typeof createBusinessHoursSchema>
>
