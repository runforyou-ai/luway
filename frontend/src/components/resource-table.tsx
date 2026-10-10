/** 管理列表的数据表格和行操作菜单。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import {
  RowActionsMenu,
  type ResourceRowAction,
} from "@/components/row-actions-menu"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"

/** 一列的表头、单元格和样式；className 同时作用于表头和单元格，headerClassName 与 cellClassName 分别追加。 */
type ResourceTableColumn<T> = {
  key: string
  header: ReactNode
  className?: string
  headerClassName?: string
  cellClassName?: string
  cell: (row: T) => ReactNode
}

export type { ResourceRowAction } from "@/components/row-actions-menu"

/** 按列定义渲染表头和单元格，空列表展示占位行；给出 onRowActivate 时整行可点击或用回车触发，canActivateRow 返回 false 的行不可进入，最右侧固定保留操作列，有操作的行可右键或点「⋯」打开同一份操作菜单，无操作的行保留等宽占位，默认隐藏表头，showHeader 用于需要列标题对比数据的列表。 */
export function ResourceTable<T>({
  columns,
  rows,
  rowKey,
  empty,
  rowActions,
  onRowActivate,
  canActivateRow,
  showHeader = false,
}: {
  columns: readonly ResourceTableColumn<T>[]
  rows: readonly T[]
  rowKey: (row: T) => string
  empty: ReactNode
  rowActions?: (row: T) => ResourceRowAction[]
  onRowActivate?: (row: T) => void
  canActivateRow?: (row: T) => boolean
  showHeader?: boolean
}) {
  const { t } = useTranslation("common")

  return (
    <Table>
      {showHeader ? (
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {columns.map((column) => (
              <TableHead
                key={column.key}
                className={cn(column.className, column.headerClassName)}
              >
                {column.header}
              </TableHead>
            ))}
            <TableHead className="w-px">
              {rowActions ? t("table.actions") : null}
            </TableHead>
          </TableRow>
        </TableHeader>
      ) : null}
      <TableBody>
        {rows.length === 0 ? (
          // 占位行与数据行同高，添加首条数据时列表高度不变。
          <TableRow className="h-[65px] hover:bg-transparent">
            <TableCell
              colSpan={columns.length + 1}
              className="text-center text-muted-foreground"
            >
              {empty}
            </TableCell>
          </TableRow>
        ) : (
          rows.map((row) => (
            <ResourceTableRow
              key={rowKey(row)}
              row={row}
              columns={columns}
              actions={rowActions?.(row)}
              onRowActivate={
                canActivateRow && !canActivateRow(row) ? undefined : onRowActivate
              }
            />
          ))
        )}
      </TableBody>
    </Table>
  )
}

/** 渲染一行数据；actions 非空时整行右键和行尾「⋯」按钮打开同一份操作菜单。 */
function ResourceTableRow<T>({
  row,
  columns,
  actions,
  onRowActivate,
}: {
  row: T
  columns: readonly ResourceTableColumn<T>[]
  actions: ResourceRowAction[] | undefined
  onRowActivate?: (row: T) => void
}) {
  return (
    <RowActionsMenu actions={actions ?? []}>
      {({ moreButton, menuOpen }) => (
        <TableRow
          // 菜单打开期间保持该行的悬停底色，标明菜单作用的行。
          className={cn(
            "group/row h-[65px]",
            menuOpen && "bg-muted/40",
            onRowActivate && "cursor-pointer",
          )}
          tabIndex={onRowActivate ? 0 : undefined}
          onClick={onRowActivate ? () => onRowActivate(row) : undefined}
          onKeyDown={
            onRowActivate
              ? (event) => {
                  if (event.target !== event.currentTarget) return
                  if (event.key !== "Enter" && event.key !== " ") return
                  event.preventDefault()
                  onRowActivate(row)
                }
              : undefined
          }
        >
          {columns.map((column) => (
            <TableCell
              key={column.key}
              className={cn(column.className, column.cellClassName)}
            >
              {column.cell(row)}
            </TableCell>
          ))}
          {/* 无操作的行用「⋯」按钮尺寸的占位，各列表的行尾列对齐。 */}
          <TableCell className="w-px whitespace-nowrap">
            <div className="flex justify-end">
              {moreButton ?? <span aria-hidden="true" className="block size-7" />}
            </div>
          </TableCell>
        </TableRow>
      )}
    </RowActionsMenu>
  )
}
