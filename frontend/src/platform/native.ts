/** 调用原生端本机能力：包装 Wails 绑定的本机能力服务，把异常转换为前端错误，并订阅本机能力发出的界面事件。 */
import { Events } from "@wailsio/runtime"

import type * as Models from "../../bindings/github.com/runforyou-ai/luway/internal/native/models"
import * as Native from "../../bindings/github.com/runforyou-ai/luway/internal/native/service"
import { normalizeError } from "@/api/client"

export {
  ClientUpdateState,
  LocalMCPServerType,
  LocalSkillSource,
  LocalToolchainFailure,
  LocalToolchainState,
  NotificationPermissionStatus,
  type AttachedComputer,
  type ClientUpdate,
  type ComputerAccount,
  type ComputerAttachment,
  type ComputerIdentity,
  type ImageFile,
  type LocalComputer,
  type LocalMCPServerInput,
  type LocalSkill,
  type LocalSkillInstallInput,
  type MessageNotificationInput,
  type UnreadIndicatorState,
} from "../../bindings/github.com/runforyou-ai/luway/internal/native/models"

// 与 internal/native/types.go 中的事件名保持一致。
const notificationOpenedEventName = "app:notification:opened"
const serverLinkOpenedEventName = "app:server-link:opened"
const localComputerChangedEventName = "app:local-computer:changed"

/** 递归把 Wails 生成类型中的可空切片视为数组，本机能力服务返回的切片均为数组。 */
type NonNullArrays<T> = [NonNullable<T>] extends [readonly (infer Element)[]]
  ? NonNullArrays<Element>[]
  : T extends object
    ? { [Key in keyof T]: NonNullArrays<T[Key]> }
    : T

/** 本机为 AI 员工提供的运行环境、本地 MCP 服务与技能。 */
export type LocalEnvironment = NonNullArrays<Models.LocalEnvironment>

/** 这台电脑上的一个本地 MCP 服务。 */
export type LocalMCPServer = NonNullArrays<Models.LocalMCPServer>

/** 本机技能来源的有效取值。 */
export type LocalSkillSourceId = Exclude<Models.LocalSkillSource, Models.LocalSkillSource.$zero>

/** 把本机能力调用包装为异常已转换为前端错误的函数。 */
function nativeCall<A extends unknown[], R>(operation: (...args: A) => PromiseLike<R>) {
  return async (...args: A): Promise<NonNullArrays<R>> => {
    try {
      return (await operation(...args)) as NonNullArrays<R>
    } catch (error) {
      throw normalizeError(error)
    }
  }
}

/** 把账号语言同步到托盘、应用菜单与本机能力的错误文案。 */
export const setNativeLocale = nativeCall(Native.SetLocale)

/** 使用原生文件对话框选择图片，用户取消时返回空文件。 */
export const selectImage = nativeCall(Native.SelectImage)

/** 在原生端打开保存对话框并写入文本文件，用户取消时返回 false。 */
export const saveNativeTextFile = nativeCall(Native.SaveTextFile)

/** 在桌面端独立窗口打开指定会话，同一会话已打开时聚焦现有窗口。 */
export const openConversationWindow = nativeCall(Native.OpenConversationWindow)

/** 关闭全部会话独立窗口，登录会话变化时由主窗口调用。 */
export const closeConversationWindows = nativeCall(Native.CloseConversationWindows)

/** 读取当前设备的通知权限状态。 */
export const checkNotificationPermission = nativeCall(Native.CheckNotificationPermission)

/** 申请当前设备的通知权限。 */
export const requestNotificationPermission = nativeCall(Native.RequestNotificationPermission)

/** 投递一条新消息通知。 */
export const sendNativeMessageNotification = nativeCall(Native.SendMessageNotification)

/** 读取并清除原生端最近一次被点击的通知要打开的页面地址。 */
export const takeOpenedNotificationPath = nativeCall(Native.TakeOpenedNotificationPath)

/** 订阅原生端通知被点击，返回取消订阅函数。 */
export function onNotificationOpened(listener: () => void) {
  return Events.On(notificationOpenedEventName, () => listener())
}

/** 同步当前设备的未读数和托盘提醒状态。 */
export const updateUnreadIndicator = nativeCall(Native.UpdateUnreadIndicator)

/** 读取并清除原生端最近一次被连接链接唤起时携带的部署地址。 */
export const takeOpenedServerLink = nativeCall(Native.TakeOpenedServerLink)

/** 订阅原生端被连接链接唤起，返回取消订阅函数。 */
export function onServerLinkOpened(listener: () => void) {
  return Events.On(serverLinkOpenedEventName, () => listener())
}

/** 检查服务器提供的客户端版本，较新时下载更新包并校验签名；当前端不能从服务器更新时返回 unsupported。 */
export const prepareClientUpdate = nativeCall(Native.PrepareClientUpdate)

/** 退出应用并以已准备好的新版本重新启动。 */
export const restartClientUpdate = nativeCall(Native.RestartClientUpdate)

/** 读取本机执行器安装标识与电脑名称，本机不作为电脑时为空标识。 */
export const getComputerIdentity = nativeCall(Native.GetComputerIdentity)

/** 读取本机在指定服务器上为指定账号注册的各工作区电脑。 */
export const listAttachedComputers = nativeCall(Native.ListAttachedComputers)

/** 保存为一个工作区注册得到的电脑凭据，本机随即以之执行派发给这台电脑的操作。 */
export const attachComputer = nativeCall(Native.AttachComputer)

/** 删除本机在指定服务器上为指定账号注册、但工作区不在保留列表中的电脑。 */
export const detachComputers = nativeCall(Native.DetachComputers)

/** 读取本机在指定工作区为指定账号注册的电脑与运行环境的准备状态。 */
export const currentNativeComputer = nativeCall(Native.CurrentComputer)

/** 订阅原生端本机电脑状态变化，返回取消订阅函数。 */
export function onLocalComputerChanged(listener: () => void) {
  return Events.On(localComputerChangedEventName, () => listener())
}

/** 读取本机为 AI 员工提供的运行环境、本地 MCP 服务与技能。 */
export const getLocalEnvironment = nativeCall(Native.GetLocalEnvironment)

/** 把本机运行环境更新到下载源的最新版本。 */
export const updateLocalToolchain = nativeCall(Native.UpdateLocalToolchain)

/** 卸载本机运行环境，手动重新安装前保持未安装。 */
export const uninstallLocalToolchain = nativeCall(Native.UninstallLocalToolchain)

/** 重新安装已卸载的本机运行环境。 */
export const installLocalToolchain = nativeCall(Native.InstallLocalToolchain)

/** 在系统文件管理器中打开本机运行环境的安装位置。 */
export const openLocalToolchainFolder = nativeCall(Native.OpenLocalToolchainFolder)

/** 试启动并添加这台电脑上的本地 MCP 服务，同名服务被替换。 */
export const addLocalMCPServer = nativeCall(Native.AddLocalMCPServer)

/** 删除这台电脑上的本地 MCP 服务。 */
export const removeLocalMCPServer = nativeCall(Native.RemoveLocalMCPServer)

/** 从来源把技能安装到这台电脑，同名技能被替换。 */
export const installLocalSkill = nativeCall(Native.InstallLocalSkill)

/** 删除 AI 员工安装在这台电脑上的技能。 */
export const removeLocalSkill = nativeCall(Native.RemoveLocalSkill)
