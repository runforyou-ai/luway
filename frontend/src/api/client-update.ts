/** 桌面端应用内更新调用与更新包下载进度。 */
import { Events } from "@wailsio/runtime"

import {
  CheckClientUpdate,
  InstallClientUpdate,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

export type { ClientUpdate } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"

// Wails 更新器下载更新包时广播的进度事件。
const downloadProgressEventName = "wails:updater:download-progress"

/** 读取本机客户端版本与所连接服务器提供的较新客户端版本。 */
export const checkClientUpdate = bind(CheckClientUpdate)

/** 下载并安装所连接服务器提供的客户端更新，完成后应用重启。 */
export const installClientUpdate = bind(InstallClientUpdate)

/** 订阅更新包下载进度，返回取消订阅函数；total 为 0 表示总大小未知。 */
export function onClientUpdateProgress(listener: (progress: { written: number; total: number }) => void) {
  return Events.On(downloadProgressEventName, (event) => listener(event.data as { written: number; total: number }))
}
