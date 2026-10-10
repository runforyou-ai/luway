/** 列表行和导航树节点共用的行操作菜单：整行右键与行尾「⋯」按钮打开同一份操作。 */
import { useRef, useState, type ReactElement, type ReactNode } from "react"
import { flushSync } from "react-dom"
import { MoreHorizontalIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"

/** 一行的一项操作，同时出现在右键菜单和行尾「⋯」菜单中；separatorBefore 在该项前加分隔线。 */
export type ResourceRowAction = {
  key: string
  label: string
  onSelect: () => void
  disabled?: boolean
  destructive?: boolean
  separatorBefore?: boolean
}

/**
 * children 返回整行元素并放入 moreButton，该元素作为右键菜单的触发区，需带 group/row 类；
 * 「⋯」在悬停行、键盘聚焦行内元素和菜单打开期间显示，菜单项执行后焦点交给「⋯」按钮。
 * actions 为空时只渲染行，不挂菜单。
 */
export function RowActionsMenu({
  actions,
  buttonSize = "icon-sm",
  buttonClassName,
  children,
}: {
  actions: ResourceRowAction[]
  buttonSize?: "icon-sm" | "icon-xs"
  buttonClassName?: string
  children: (menu: { moreButton: ReactNode; menuOpen: boolean }) => ReactElement
}) {
  const { t } = useTranslation("common")
  const [contextOpen, setContextOpen] = useState(false)
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const moreButtonRef = useRef<HTMLButtonElement>(null)
  const actionSelected = useRef(false)
  const menuOpen = contextOpen || dropdownOpen
  const moreLabel = t("actions.moreActions")

  if (actions.length === 0) return children({ moreButton: null, menuOpen: false })

  /** 同步关闭菜单解除焦点锁定，把焦点交给「⋯」按钮后执行操作，操作打开的弹窗关闭时焦点回到该按钮。 */
  function selectAction(event: Event, action: ResourceRowAction) {
    event.preventDefault()
    actionSelected.current = true
    flushSync(() => {
      setContextOpen(false)
      setDropdownOpen(false)
    })
    moreButtonRef.current?.focus()
    action.onSelect()
  }

  /** 菜单关闭后焦点回到「⋯」按钮；已执行操作时焦点已交给按钮或操作打开的弹窗。 */
  function restoreFocus(event: Event) {
    event.preventDefault()
    if (!actionSelected.current) moreButtonRef.current?.focus()
    actionSelected.current = false
  }

  const moreButton = (
    // React 事件沿组件树冒泡，按钮和经 Portal 渲染的菜单的点击在这里截停，不触发整行的点击。
    <div
      className="contents"
      onClick={(event) => event.stopPropagation()}
    >
      <DropdownMenu open={dropdownOpen} onOpenChange={setDropdownOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            ref={moreButtonRef}
            type="button"
            variant="ghost"
            size={buttonSize}
            aria-label={moreLabel}
            title={moreLabel}
            // 隐藏时不接收点击，行尾内容照常点击进入；显示时才可点开菜单。
            className={cn(
              "pointer-events-none opacity-0 group-hover/row:pointer-events-auto group-hover/row:opacity-100 group-focus-visible/row:pointer-events-auto group-focus-visible/row:opacity-100 group-has-[:focus-visible]/row:pointer-events-auto group-has-[:focus-visible]/row:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100",
              menuOpen && "pointer-events-auto opacity-100",
              buttonClassName,
            )}
          >
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          onCloseAutoFocus={restoreFocus}
          // 菜单内的右键只作用于菜单本身，不打开整行的右键菜单。
          onContextMenu={(event) => {
            event.preventDefault()
            event.stopPropagation()
          }}
        >
          {actions.map((action, index) => (
            <DropdownMenuActionItem
              key={action.key}
              action={action}
              separated={Boolean(action.separatorBefore) && index > 0}
              onSelect={selectAction}
            />
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )

  return (
    <ContextMenu open={contextOpen} onOpenChange={setContextOpen}>
      <ContextMenuTrigger asChild>
        {children({ moreButton, menuOpen })}
      </ContextMenuTrigger>
      <ContextMenuContent onCloseAutoFocus={restoreFocus}>
        {actions.map((action, index) => (
          <ContextMenuActionItem
            key={action.key}
            action={action}
            separated={Boolean(action.separatorBefore) && index > 0}
            onSelect={selectAction}
          />
        ))}
      </ContextMenuContent>
    </ContextMenu>
  )
}

/** 行尾「⋯」菜单中的一项操作，separated 时在前面加分隔线。 */
function DropdownMenuActionItem({
  action,
  separated,
  onSelect,
}: {
  action: ResourceRowAction
  separated: boolean
  onSelect: (event: Event, action: ResourceRowAction) => void
}) {
  return (
    <>
      {separated ? <DropdownMenuSeparator /> : null}
      <DropdownMenuItem
        disabled={action.disabled}
        destructive={action.destructive}
        onSelect={(event) => onSelect(event, action)}
      >
        {action.label}
      </DropdownMenuItem>
    </>
  )
}

/** 整行右键菜单中的一项操作，separated 时在前面加分隔线。 */
function ContextMenuActionItem({
  action,
  separated,
  onSelect,
}: {
  action: ResourceRowAction
  separated: boolean
  onSelect: (event: Event, action: ResourceRowAction) => void
}) {
  return (
    <>
      {separated ? <ContextMenuSeparator /> : null}
      <ContextMenuItem
        disabled={action.disabled}
        destructive={action.destructive}
        onSelect={(event) => onSelect(event, action)}
      >
        {action.label}
      </ContextMenuItem>
    </>
  )
}
