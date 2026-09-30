/** 主内容区的标准页头。 */
import type { ReactNode } from "react"
import { ArrowLeftIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { SelectableText } from "@/components/selectable-text"
import { Button } from "@/components/ui/button"

/**
 * 显示统一的标题、说明、前置内容、返回入口和操作区。
 * 标题距内容区顶边固定 30px；返回入口位于标题左侧，返回入口和操作区只在标题行高度内垂直居中。
 */
export function PageHeader({
  title,
  description,
  beforeTitle,
  backTo,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  beforeTitle?: ReactNode
  backTo?: string
  children?: ReactNode
}) {
  const { t } = useTranslation("common")
  const backLabel = t("actions.back")

  return (
    <header
      data-slot="page-header"
      className="app-page-gutter flex shrink-0 flex-wrap items-start gap-2.5 pt-[30px] pb-3.5 select-none md:flex-nowrap"
    >
      {beforeTitle}
      {backTo ? (
        <div className="-ml-1.5 flex h-7 shrink-0 items-center">
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-foreground"
            asChild
          >
            <Link to={backTo} aria-label={backLabel} title={backLabel}>
              <ArrowLeftIcon />
            </Link>
          </Button>
        </div>
      ) : null}
      <div className="mr-auto min-w-0 flex-1">
        <h2
          data-slot="page-header-title"
          className="w-fit max-w-full truncate text-2xl font-semibold tracking-tight"
        >
          <SelectableText>{title}</SelectableText>
        </h2>
        {description ? (
          <p
            data-slot="page-header-description"
            className="mt-1 truncate text-sm text-muted-foreground"
          >
            {description}
          </p>
        ) : null}
      </div>
      {children ? (
        <div
          data-slot="page-header-actions"
          className="flex h-7 shrink-0 items-center gap-2"
        >
          {children}
        </div>
      ) : null}
    </header>
  )
}
