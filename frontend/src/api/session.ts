/** 读取启动入口、登录身份并提供会话状态路由。 */
import { SessionState } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import {
  GetSyncHeads,
  InstallationStatus,
  LoadIdentity,
  LoadStartup,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 读取部署的安装状态和注册开关。 */
export const loadInstallationStatus = bind(InstallationStatus)

/** 读取初始化、服务器连接或就绪入口。 */
export const loadStartup = bind(LoadStartup)

/** 读取当前账号在当前工作区中的成员身份。 */
export const loadIdentity = bind(LoadIdentity)

/** 读取当前用户可见会话与身份资料的同步探针值。 */
export const getSyncHeads = bind(GetSyncHeads)

/** 将会话状态映射为根路径下的账号级路由。 */
export function sessionPath(state: string) {
  switch (state) {
    case SessionState.SessionStateLogin:
      return "/login"
    case SessionState.SessionStateSetup:
      return resolveAppPlatform() === "web" ? "/setup" : "/connect"
    case SessionState.SessionStateConnect:
      return "/connect"
    case SessionState.SessionStateWorkspace:
      return "/workspaces"
    case SessionState.SessionStateUpgrade:
      return "/upgrade"
    default:
      return null
  }
}
