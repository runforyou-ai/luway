/** 移动端列表的筛选摘要按钮与底部筛选面板。 */
import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { focusDialogContainer } from "@/lib/dialog-focus"

/** 以摘要按钮打开底部面板，打开时由调用方载入草稿，应用后关闭；取消时保留原筛选；给出 onReset 时提供把草稿恢复默认的重置按钮。 */
export function MobileFilterSheet({
  summary,
  onOpen,
  onApply,
  onReset,
  onOpenChange,
  children,
}: {
  summary: string
  onOpen: () => void
  onApply: () => void
  onReset?: () => void
  onOpenChange?: (open: boolean) => void
  children: ReactNode
}) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const [open, setOpen] = useState(false)

  /** 同步面板开关并通知列表暂停滑动与刷新定位。 */
  function changeOpen(next: boolean) {
    if (next) onOpen()
    setOpen(next)
    onOpenChange?.(next)
  }

  return (
    <div className="flex h-11 shrink-0 items-center border-b">
      <Sheet open={open} onOpenChange={changeOpen}>
        <SheetTrigger asChild>
          <Button
            variant="ghost"
            className="min-h-11 min-w-0 max-w-full justify-start overflow-hidden px-4 text-xs"
          >
            <span className="min-w-0 truncate">
              {t("filterSummary", { summary: summary || t("inbox:filterAll") })}
            </span>
          </Button>
        </SheetTrigger>
        <SheetContent
          side="bottom"
          showCloseButton={false}
          aria-describedby={undefined}
          className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
          onOpenAutoFocus={focusDialogContainer}
        >
          <SheetHeader className="flex-row items-center border-b">
            <SheetTitle className="flex-1">{t("inbox:filterLabel")}</SheetTitle>
            <SheetClose asChild>
              <Button variant="ghost" className="min-h-11">
                {t("common:actions.cancel")}
              </Button>
            </SheetClose>
          </SheetHeader>
          <div className="overflow-y-auto p-4 space-y-9">
            {children}
            <div className={onReset ? "grid grid-cols-2 gap-2" : undefined}>
              {onReset ? (
                <Button variant="outline" className="min-h-11" onClick={onReset}>
                  {t("inbox:filterReset")}
                </Button>
              ) : null}
              <Button
                className="min-h-11 w-full"
                onClick={() => {
                  onApply()
                  changeOpen(false)
                }}
              >
                {t("apply")}
              </Button>
            </div>
          </div>
        </SheetContent>
      </Sheet>
    </div>
  )
}
