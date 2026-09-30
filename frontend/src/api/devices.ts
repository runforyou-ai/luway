/** 本机设备调用。 */
import { Events } from "@wailsio/runtime"

import {
  CurrentDevice,
  GetLocalEnvironment,
  InstallLocalToolchain,
  ListDevices,
  OpenLocalToolchainFolder,
  RemoveLocalMCPServer,
  RemoveLocalSkill,
  RevokeDevice,
  UninstallLocalToolchain,
  UpdateLocalToolchain,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import {
  DevicePlatform,
  LocalAgentKind,
  LocalSkillSource,
  type Device,
  type DeviceList,
  type LocalEnvironment,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

// 与 internal/appservice/types_device.go 中的 LocalDeviceChangedEventName 保持一致。
const localDeviceChangedEventName = "app:local-device:changed"

type DevicePlatformId = Exclude<DevicePlatform, DevicePlatform.$zero>

export type DeviceData = Omit<NonNullArrays<Device>, "platform" | "localAgents"> & {
  platform: DevicePlatformId
  localAgents: Exclude<LocalAgentKind, LocalAgentKind.$zero>[]
}

type DeviceListData = Omit<NonNullArrays<DeviceList>, "devices"> & {
  devices: DeviceData[]
}

const listDevicesBound = bind(ListDevices)

/** 读取当前用户已注册的设备列表。 */
export function listDevices() {
  return listDevicesBound() as Promise<DeviceListData>
}

/** 撤销当前用户的设备。 */
export const revokeDevice = bind(RevokeDevice)

/** 读取本机在当前企业服务器上的设备注册状态与 Agent 运行环境。 */
export const currentDevice = bind(CurrentDevice)

type LocalSkillSourceId = Exclude<LocalSkillSource, LocalSkillSource.$zero>

export type LocalSkillData = Omit<NonNullArrays<LocalEnvironment>["skills"][number], "source"> & {
  source: LocalSkillSourceId
}

export type LocalEnvironmentData = Omit<NonNullArrays<LocalEnvironment>, "skills"> & {
  skills: LocalSkillData[]
}

export type LocalMCPServerData = LocalEnvironmentData["mcpServers"][number]

const getLocalEnvironmentBound = bind(GetLocalEnvironment)

/** 读取本机为助理提供的运行环境、本地 MCP 服务与技能。 */
export function getLocalEnvironment() {
  return getLocalEnvironmentBound() as Promise<LocalEnvironmentData>
}

/** 把本机运行环境更新到下载源的最新版本。 */
export const updateLocalToolchain = bind(UpdateLocalToolchain)

/** 卸载本机运行环境，重新安装前不再自动安装。 */
export const uninstallLocalToolchain = bind(UninstallLocalToolchain)

/** 重新安装已卸载的本机运行环境。 */
export const installLocalToolchain = bind(InstallLocalToolchain)

/** 在系统文件管理器中打开本机运行环境的安装位置。 */
export const openLocalToolchainFolder = bind(OpenLocalToolchainFolder)

/** 删除这台电脑上的本地 MCP 服务。 */
export const removeLocalMCPServer = bind(RemoveLocalMCPServer)

/** 删除助理安装在这台电脑上的技能。 */
export const removeLocalSkill = bind(RemoveLocalSkill)

/** 订阅原生端本机设备状态变化，返回取消订阅函数。 */
export function onLocalDeviceChanged(listener: () => void) {
  return Events.On(localDeviceChangedEventName, () => listener())
}
