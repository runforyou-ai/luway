/** 账号级页面的居中外壳：品牌、标题、说明和底部账号信息。 */
import type { ReactNode } from "react"

import { useBrandName } from "@/lib/brand"

/** 在页面中央渲染账号级页面内容。 */
export function AccountShell({
  title,
  description,
  leading,
  footer,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  leading?: ReactNode
  footer?: ReactNode
  children: ReactNode
}) {
  const productName = useBrandName()
  return (
    <main className="flex min-h-dvh w-full items-center justify-center px-6 pt-[max(2.5rem,env(safe-area-inset-top))] pb-[max(2.5rem,env(safe-area-inset-bottom))]">
      <div className="w-full max-w-[400px]">
        <p className="mb-8 text-center text-sm font-semibold tracking-tight text-muted-foreground">{productName}</p>
        <div className="mb-6">
          {leading}
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {description ? <p className="mt-1.5 text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {children}
        {footer ? <div className="mt-8 text-center text-xs text-muted-foreground">{footer}</div> : null}
      </div>
    </main>
  )
}
