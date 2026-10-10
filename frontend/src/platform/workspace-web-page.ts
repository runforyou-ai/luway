/** 在系统浏览器中打开当前服务器上的工作区 Web 端页面。 */
import { serverURL } from "@/api/client"
import { webAppPath, workspaceHref } from "@/lib/workspace-route"
import { openExternalURL } from "@/platform/system"

/** 在系统浏览器中打开工作区内的 Web 端页面，path 为工作区内的相对根路径。 */
export async function openWorkspaceWebPage(slug: string, path: string) {
  await openExternalURL(`${serverURL()}${webAppPath}#${workspaceHref(slug, path)}`)
}
