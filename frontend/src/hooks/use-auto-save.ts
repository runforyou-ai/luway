/** 表单值变化后的自动保存。 */
import { useEffect, useRef, type RefObject } from "react"
import type { FieldValues, UseFormReturn } from "react-hook-form"
import type { ZodType } from "zod"
import { useMountedRef } from "@/hooks/use-mounted-ref"

/**
 * 启用时，值变化并通过校验后按延迟提交，与上次保存结果相同时跳过；
 * 保存串行执行，保存期间产生的改动在本次完成后补发一次。校验不通过时静默等待，
 * 由字段自身的失焦校验提示用户。save 返回 false 表示未保存成功，下次改动时会重新提交。
 * 停用或卸载前仍在等待的改动按当时生效的 save 立即提交；discarded 为 true 时（用户已确认放弃修改）不提交。
 */
export function useAutoSave<T extends FieldValues>({
  form,
  schema,
  save,
  enabled = true,
  delay = 600,
  discarded,
}: {
  form: UseFormReturn<T>
  schema: ZodType<T>
  save: (values: T) => Promise<boolean>
  enabled?: boolean
  delay?: number
  discarded?: RefObject<boolean>
}) {
  // 提交后才更新回调，停用或卸载时的清理仍使用切换前那次渲染的 save。
  const latest = useRef({ schema, save })
  useEffect(() => {
    latest.current = { schema, save }
  })
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const saving = useRef(false)
  const queued = useRef<"changed" | "forced" | null>(null)
  const saved = useRef(JSON.stringify(form.getValues()))
  const savedVersion = useRef(0)
  const mounted = useMountedRef()

  /** 保存当前值；force 为 true 时即使与上次保存结果相同也提交。 */
  async function flush(force = false) {
    if (saving.current) {
      if (force || queued.current === null) queued.current = force ? "forced" : "changed"
      return
    }
    const parsed = latest.current.schema.safeParse(form.getValues())
    if (!parsed.success) return
    const serialized = JSON.stringify(parsed.data)
    if (!force && serialized === saved.current) return

    saving.current = true
    const version = savedVersion.current
    try {
      if (await latest.current.save(parsed.data)) {
        if (version === savedVersion.current) saved.current = serialized
      }
    } finally {
      saving.current = false
      const next = queued.current
      queued.current = null
      if (next && !discarded?.current) void flush(next === "forced")
    }
  }
  const flushRef = useRef(flush)
  flushRef.current = flush

  useEffect(() => {
    if (!enabled) return
    const subscription = form.watch(() => {
      clearTimeout(timer.current)
      timer.current = setTimeout(() => {
        timer.current = undefined
        void flushRef.current()
      }, delay)
    })
    return () => {
      subscription.unsubscribe()
      if (timer.current === undefined) return
      clearTimeout(timer.current)
      timer.current = undefined
      if (!discarded?.current) void flushRef.current()
    }
  }, [form, enabled, delay, discarded])

  return {
    /** 服务端返回新值后同步保存基准，回填值与基准相同时跳过保存。 */
    markSaved(values: T) {
      saved.current = JSON.stringify(values)
      savedVersion.current += 1
    },
    /** 确认已保存快照，仅在当前值仍与提交值相同时回填服务端结果；回填值与当前值相同时只更新表单基准，保留数组行与输入焦点。 */
    acceptSaved(submitted: T, values: T = submitted) {
      saved.current = JSON.stringify(values)
      savedVersion.current += 1
      const current = latest.current.schema.safeParse(form.getValues())
      if (
        mounted.current &&
        current.success &&
        JSON.stringify(current.data) === JSON.stringify(submitted)
      ) {
        const unchanged = JSON.stringify(form.getValues()) === JSON.stringify(values)
        form.reset(values, unchanged ? { keepValues: true } : undefined)
        return true
      }
      return false
    },
    /** 立即排队保存一次并与自动保存串行；force 用于表单值之外的改动（如成员分配），值未变也提交。 */
    saveNow(force = false) {
      clearTimeout(timer.current)
      timer.current = undefined
      void flushRef.current(force)
    },
  }
}
