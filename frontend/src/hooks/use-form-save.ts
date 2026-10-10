/** 新建页提交与编辑页自动保存共用的保存流程。 */
import { useRef } from "react"
import type { FieldValues, UseFormReturn } from "react-hook-form"
import type { ZodType } from "zod"

import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"

/**
 * autoSave 为 true 时边改边存，表单提交也按自动保存处理并留在当前页；否则由表单提交，
 * 有改动时登记离开确认。请求和缓存失效由 save 完成；保存成功且页面仍在时先调用 onSaved，
 * 非自动保存再重置表单并调用 onSubmitted（提示与跳转）。失败统一恢复会话或提示错误，未给出 errorMessage 时非接口错误提示无法连接服务器，
 * 离开页面后提交的改动失败时同样提示。
 * hold 返回 true 时暂不保存（如等待确认），之后由调用方用 commit 排队提交；
 * unsaved 为 true 时自动保存页也登记离开确认，用于暂缓中的改动。
 */
export function useFormSave<T extends FieldValues, R>({
  form,
  schema,
  autoSave,
  save,
  hold,
  unsaved = false,
  onSaved,
  onSubmitted,
  savedValues,
  errorMessage,
  errorFields,
  logLabel,
}: {
  form: UseFormReturn<T>
  schema: ZodType<T>
  autoSave: boolean
  save: (values: T) => Promise<R>
  hold?: (values: T, autoSaved: boolean) => boolean
  unsaved?: boolean
  onSaved?: (result: R, values: T) => void
  onSubmitted?: (result: R, values: T) => void
  savedValues?: (result: R, values: T) => T
  errorMessage?: string
  errorFields?: string[]
  logLabel: string
}) {
  const reportRequestError = useRequestErrorReporter()
  const pendingSave = useRef<Promise<boolean> | null>(null)
  const { mounted, dirty, discarded } = useFormLifetime(
    (!autoSave && form.formState.isDirty) || unsaved,
  )
  const { acceptSaved, markSaved, saveNow } = useAutoSave({
    form,
    schema,
    enabled: autoSave,
    discarded,
    save: (values) => submit(values, true),
  })

  /** 展示保存失败；会话失效时转入会话入口并返回 true，调用方据此停止后续导航。 */
  function reportError(error: unknown) {
    return reportRequestError(error, { log: logLabel, fallback: errorMessage, fields: errorFields })
  }

  /** 跳过暂缓判断并排队保存，返回是否保存成功。 */
  function commit(values: T, autoSaved = false) {
    const pending = (pendingSave.current ?? Promise.resolve()).then(async () => {
      try {
        const result = await save(values)
        if (!mounted.current) return true
        onSaved?.(result, values)
        if (autoSaved) {
          acceptSaved(values, savedValues?.(result, values) ?? values)
        } else {
          dirty.current = false
          form.reset(values)
          onSubmitted?.(result, values)
        }
        return true
      } catch (error) {
        if (mounted.current || autoSaved) reportError(error)
        return false
      }
    })
    pendingSave.current = pending
    return pending
  }

  /** 保存表单值；暂缓时视为未保存，后续改动或确认后再提交。 */
  async function submit(values: T, autoSaved = false) {
    if (hold?.(values, autoSaved)) return false
    return commit(values, autoSaved)
  }

  return {
    // 自动保存页按回车等方式提交时立即保存，与自动保存串行且留在当前页。
    submit: (values: T) => (autoSave ? saveNow() : submit(values)),
    commit,
    markSaved,
    saveNow,
    reportError,
    mounted,
  }
}
