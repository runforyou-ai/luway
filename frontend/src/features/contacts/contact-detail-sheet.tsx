/** 通讯录详情侧栏的共享框架。 */
import { useRef, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { LoadingIndicator } from "@/components/loading-indicator"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

/** 渲染详情侧栏的标题、加载态和内容区。 */
export function ContactDetailSheet({
  open,
  onClose,
  title,
  description,
  loading,
  children,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  description: ReactNode
  loading: boolean
  children: ReactNode
}) {
  const { t } = useTranslation("common")
  const detailTitleRef = useRef<HTMLHeadingElement>(null)

  return (
    <Sheet open={open} onOpenChange={(next) => !next && onClose()}>
      <SheetContent
        className="w-full gap-0 p-0 sm:max-w-xl"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          detailTitleRef.current?.focus()
        }}
      >
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle
            ref={detailTitleRef}
            tabIndex={-1}
            className="outline-none"
          >
            {title}
          </SheetTitle>
          <SheetDescription>{description}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-6">
            {loading ? (
              <LoadingIndicator className="min-h-40 justify-center">
                {t("status.loading")}
              </LoadingIndicator>
            ) : (
              children
            )}
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
