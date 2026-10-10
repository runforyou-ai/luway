/** 工作区完整访问地址的读取与只读展示。 */
import { serverURL } from "@/api"
import { cn } from "@/lib/utils"
import { webAppPath, workspaceHref } from "@/lib/workspace-route"

/** 返回工作区完整访问地址。 */
export function useWorkspaceAddress(slug: string) {
  return `${serverURL()}${webAppPath}#${workspaceHref(slug, "/")}`
}

/** 展示可选中复制的工作区访问地址，长地址保留标识并在悬停时显示全文。 */
export function WorkspaceAddress({ slug, className }: { slug: string; className?: string }) {
  const address = useWorkspaceAddress(slug)
  const prefix = address.slice(0, address.length - slug.length)
  return (
    <span className={cn("flex min-w-0 text-xs text-muted-foreground", className)} title={address}>
      <span className="min-w-0 truncate">{prefix}</span>
      <span className="max-w-full shrink-0 truncate">{slug}</span>
    </span>
  )
}
