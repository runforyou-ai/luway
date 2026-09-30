/** 模型服务供应商的品牌标志。 */
import { ServerIcon } from "lucide-react"

import type { AIProviderBrandId } from "@/api"
import { aiProviderBrandConfigs } from "@/features/integrations/model-services/model-provider-brands"
import { cn } from "@/lib/utils"

/** 在圆角底块中展示品牌标志，没有品牌标志的通用服务显示服务器图标。 */
export function ModelProviderBrandIcon({
  brand,
  className,
}: {
  brand: AIProviderBrandId
  className?: string
}) {
  const icon = aiProviderBrandConfigs[brand].icon
  const tileClassName = cn(
    "flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background text-foreground [&>svg]:size-5",
    className,
  )
  return icon ? (
    // 标志为依赖包内置的静态 SVG。
    <span aria-hidden="true" className={tileClassName} dangerouslySetInnerHTML={{ __html: icon }} />
  ) : (
    <span aria-hidden="true" className={tileClassName}>
      <ServerIcon className="text-muted-foreground" />
    </span>
  )
}
