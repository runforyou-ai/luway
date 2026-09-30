/** 通过模态框为 AI 员工选择知识库、MCP 服务等可绑定资源。 */
import { useRef, useState, type ReactNode, type RefObject } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useResource } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"

/** 模态框中的一张可选卡片。 */
type AgentResourceOption = {
  id: string
  name: string
  detail?: ReactNode
}

/** 资源类型相关的文案。 */
type AgentResourcePickerLabels = {
  title: string
  group: string
  unconfigured: string
  /** names 为已选中且仍存在的前几项名称，列表未加载或都已失效时为空。 */
  selected: (count: number, names: string) => string
  empty: string
  loadError: string
}

/** 显示表单选择摘要，确认模态框后才修改表单。 */
export function AgentResourcePickerField<T>({
  value,
  onChange,
  disabled,
  resourceKey,
  load,
  toOptions,
  labels,
}: {
  value: string[]
  onChange: (ids: string[]) => void
  disabled: boolean
  resourceKey: QueryKey
  load: (signal: AbortSignal) => Promise<T>
  toOptions: (data: T) => AgentResourceOption[]
  labels: AgentResourcePickerLabels
}) {
  const { t } = useTranslation("agents")
  const [open, setOpen] = useState(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const resource = useResource(resourceKey, load, { staleTime: 0 })
  // 摘要最多列出前两项名称，其余用总数概括。
  const options = resource.data ? toOptions(resource.data) : []
  const names = options
    .filter((option) => value.includes(option.id))
    .slice(0, 2)
    .map((option) => option.name)
    .join(t("nameSeparator"))
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <div className="flex items-center gap-3">
        <Button
          ref={trigger}
          type="button"
          variant="outline"
          className="h-11 rounded-lg px-4"
          disabled={disabled}
          onClick={() => setOpen(true)}
        >
          {t("configure")}
        </Button>
        <span className="text-sm text-muted-foreground">
          {value.length === 0
            ? labels.unconfigured
            : labels.selected(value.length, names)}
        </span>
      </div>
      {open && (
        <AgentResourcePickerDialog
          value={value}
          trigger={trigger}
          resourceKey={resourceKey}
          load={load}
          toOptions={toOptions}
          labels={labels}
          onCancel={() => setOpen(false)}
          onConfirm={(ids) => {
            // 选择未变化时不改表单，避免触发无意义的自动保存。
            const unchanged =
              ids.length === value.length &&
              ids.every((id) => value.includes(id))
            if (!unchanged) onChange(ids)
            setOpen(false)
          }}
        />
      )}
    </Dialog>
  )
}

/** 展示资源卡片并维护本次模态框的独立草稿。 */
function AgentResourcePickerDialog<T>({
  value,
  trigger,
  resourceKey,
  load,
  toOptions,
  labels,
  onCancel,
  onConfirm,
}: {
  value: string[]
  trigger: RefObject<HTMLButtonElement | null>
  resourceKey: QueryKey
  load: (signal: AbortSignal) => Promise<T>
  toOptions: (data: T) => AgentResourceOption[]
  labels: AgentResourcePickerLabels
  onCancel: () => void
  onConfirm: (ids: string[]) => void
}) {
  const { t } = useTranslation("common")
  const [selected, setSelected] = useState(value)
  const resource = useResource(resourceKey, load, { staleTime: 0 })
  // 缓存保留接口原始数据，与其他页面共用同一个 key 时不会互相污染。
  const options = resource.data ? toOptions(resource.data) : []
  return (
    <DialogContent
      className="max-w-4xl grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden"
      aria-describedby={undefined}
      onCloseAutoFocus={(event) => {
        event.preventDefault()
        trigger.current?.focus()
      }}
    >
      <DialogHeader>
        <DialogTitle>{labels.title}</DialogTitle>
      </DialogHeader>
      <div className="min-h-48 overflow-y-auto p-1">
        {resource.loading ? (
          <LoadingIndicator className="min-h-48 justify-center">
            {t("status.loading")}
          </LoadingIndicator>
        ) : resource.error ? (
          <div className="flex min-h-48 flex-col items-center justify-center gap-3">
            <p className="text-sm text-muted-foreground">{labels.loadError}</p>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void resource.refresh()}
            >
              {t("actions.retry")}
            </Button>
          </div>
        ) : options.length === 0 ? (
          <p className="flex min-h-48 items-center justify-center text-sm text-muted-foreground">
            {labels.empty}
          </p>
        ) : (
          <div
            className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3"
            role="group"
            aria-label={labels.group}
          >
            {options.map((option) => (
              <label
                key={option.id}
                className={cn(
                  "flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border bg-card p-4 has-focus-visible:border-primary",
                  selected.includes(option.id)
                    ? "border-primary bg-primary/5"
                    : "hover:bg-accent/50",
                )}
              >
                <input
                  type="checkbox"
                  className="mt-0.5 size-4 shrink-0 accent-primary"
                  aria-label={option.name}
                  checked={selected.includes(option.id)}
                  onChange={(event) =>
                    setSelected(
                      event.target.checked
                        ? [...selected, option.id]
                        : selected.filter((id) => id !== option.id),
                    )
                  }
                />
                <span className="min-w-0 space-y-2">
                  <span
                    className="line-clamp-2 text-sm font-medium break-all"
                    title={option.name}
                  >
                    {option.name}
                  </span>
                  {option.detail ? (
                    <span className="line-clamp-2 block text-xs text-muted-foreground">
                      {option.detail}
                    </span>
                  ) : null}
                </span>
              </label>
            ))}
          </div>
        )}
      </div>
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          className="touch:min-h-11"
          onClick={onCancel}
        >
          {t("actions.cancel")}
        </Button>
        <Button
          type="button"
          className="touch:min-h-11"
          disabled={
            resource.loading || Boolean(resource.error) || resource.refreshing
          }
          onClick={() => {
            // 只把当前目录中仍存在的选中项写入表单。
            onConfirm(
              options
                .filter((option) => selected.includes(option.id))
                .map((option) => option.id)
                .sort(),
            )
          }}
        >
          {t("actions.confirm")}
        </Button>
      </div>
    </DialogContent>
  )
}
