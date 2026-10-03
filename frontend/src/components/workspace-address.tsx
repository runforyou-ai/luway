/** 工作区完整访问地址的只读展示。 */
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { resolveServerURL } from "@/lib/server-url"
import { cn } from "@/lib/utils"
import { webAppPath, workspaceHref } from "@/lib/workspace-route"

/** 展示可选中复制的工作区访问地址，长地址保留标识并在悬停时显示全文。 */
export function WorkspaceAddress({ slug, className }: { slug: string; className?: string }) {
  const serverURL = useResource(resourceKeys.serverURL(), () => resolveServerURL())
  const path = `${webAppPath}#${workspaceHref(slug, "/")}`
  const address = serverURL.data ? `${serverURL.data.replace(/\/+$/, "")}${path}` : path
  const prefix = address.slice(0, address.length - slug.length)
  return (
    <span className={cn("flex min-w-0 text-xs text-muted-foreground", className)} title={address}>
      <span className="min-w-0 truncate">{prefix}</span>
      <span className="max-w-full shrink-0 truncate">{slug}</span>
    </span>
  )
}
