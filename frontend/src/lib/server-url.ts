/** 当前客户端连接的企业服务器地址，供渠道公开回调、入口和设置页展示复用。 */
import { getServerURL } from "@/api"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 返回当前客户端连接的企业服务器地址：Web 端取页面来源，原生端取已配置的连接地址。 */
export async function resolveServerURL() {
  const serverURL =
    resolveAppPlatform() === "web"
      ? window.location.origin
      : await getServerURL()
  return serverURL.replace(/\/+$/, "")
}
