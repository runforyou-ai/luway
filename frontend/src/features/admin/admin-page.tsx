/** 平台后台页面的统一布局：紧凑页头加填满剩余高度的内容区，页面内容经 AdminPageActions 把操作放进页头右侧。 */
import { createContext, useContext, useState, type ReactNode } from "react"
import { createPortal } from "react-dom"

import { SelectableText } from "@/components/selectable-text"

/** 页头右侧操作区的挂载节点。 */
const AdminPageActionsContext = createContext<HTMLElement | null>(null)

/** 渲染页头与内容区；breadcrumb 显示在标题上方，actions 与 AdminPageActions 的内容并排显示在页头右侧。 */
export function AdminPage({
  title,
  description,
  actions,
  breadcrumb,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  breadcrumb?: ReactNode
  children: ReactNode
}) {
  const [slot, setSlot] = useState<HTMLElement | null>(null)

  return (
    <AdminPageActionsContext.Provider value={slot}>
      <div data-slot="admin-page" className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <header data-slot="admin-page-header" className="flex shrink-0 flex-wrap items-start gap-3 px-6 pt-5 pb-3 md:flex-nowrap">
          <div className="mr-auto min-w-0 flex-1">
            {breadcrumb ? <div className="mb-1 truncate text-xs text-muted-foreground">{breadcrumb}</div> : null}
            <h1 data-slot="admin-page-title" className="w-fit max-w-full truncate text-lg font-semibold tracking-tight">
              <SelectableText>{title}</SelectableText>
            </h1>
            {description ? (
              <p data-slot="admin-page-description" className="mt-0.5 truncate text-sm text-muted-foreground">
                <SelectableText>{description}</SelectableText>
              </p>
            ) : null}
          </div>
          <div data-slot="admin-page-actions" className="flex h-8 shrink-0 items-center gap-2 select-none">
            {actions}
            <span ref={setSlot} className="contents" />
          </div>
        </header>
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">{children}</div>
      </div>
    </AdminPageActionsContext.Provider>
  )
}

/** 把页面内容的操作渲染到所在 AdminPage 的页头右侧操作区。 */
export function AdminPageActions({ children }: { children: ReactNode }) {
  const target = useContext(AdminPageActionsContext)
  return target ? createPortal(children, target) : null
}
