/** 原生端从当前服务器更新客户端的调用。 */
import {
  PrepareClientUpdate,
  RestartClientUpdate,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

/** 检查当前服务器提供的客户端版本，较新时下载更新包并校验签名；当前端不能从服务器更新时返回 unsupported。 */
export const prepareClientUpdate = bind(PrepareClientUpdate)

/** 退出应用并以已准备好的新版本重新启动。 */
export const restartClientUpdate = bind(RestartClientUpdate)
