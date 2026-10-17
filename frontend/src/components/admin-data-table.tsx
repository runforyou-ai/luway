/** 平台后台用的 TanStack Table 封装：列定义、排序与紧凑行渲染，空数据时保留表头。 */
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table"
import { useState, type ReactNode } from "react"

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { cn } from "@/lib/utils"

export type AdminDataTableColumn<T> = ColumnDef<T, unknown>

/** 渲染带可选排序的后台数据表。 */
export function AdminDataTable<T>({
  columns,
  data,
  empty,
  getRowId,
  onRowActivate,
  className,
}: {
  columns: AdminDataTableColumn<T>[]
  data: T[]
  empty: ReactNode
  getRowId: (row: T) => string
  onRowActivate?: (row: T) => void
  className?: string
}) {
  const [sorting, setSorting] = useState<SortingState>([])
  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getRowId: (row) => getRowId(row),
  })

  return (
    <div className={cn("overflow-x-auto rounded-md border border-border bg-card", className)}>
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id}>
              {headerGroup.headers.map((header) => (
                <TableHead key={header.id} className="h-8 bg-muted/50 whitespace-nowrap">
                  {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                </TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {data.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={columns.length} className="py-12 text-center text-muted-foreground">
                {empty}
              </TableCell>
            </TableRow>
          ) : null}
          {table.getRowModel().rows.map((row) => (
            <TableRow
              key={row.id}
              className={onRowActivate ? "cursor-pointer" : undefined}
              onClick={onRowActivate ? () => onRowActivate(row.original) : undefined}
            >
              {row.getVisibleCells().map((cell) => (
                <TableCell key={cell.id} className="py-2">{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
