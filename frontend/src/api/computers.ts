/** 电脑与本机环境调用。 */
import { Events } from "@wailsio/runtime"

import {
  AddLocalMCPServer,
  CurrentComputer,
  GetLocalEnvironment,
  InstallLocalSkill,
  InstallLocalToolchain,
  ListComputers,
  OpenLocalToolchainFolder,
  RemoveLocalMCPServer,
  RemoveLocalSkill,
  RevokeComputer,
  UninstallLocalToolchain,
  UpdateLocalToolchain,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  ComputerPlatform,
  LocalSkillSource,
  type Computer,
  type ComputerList,
  type LocalEnvironment,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

// 与 internal/appservice/types_local_environment.go 中的 LocalComputerChangedEventName 保持一致。
const localComputerChangedEventName = "app:local-computer:changed"

type ComputerPlatformId = Exclude<ComputerPlatform, ComputerPlatform.$zero>

export type ComputerData = Omit<NonNullArrays<Computer>, "platform"> & {
  platform: ComputerPlatformId
}

type ComputerListData = Omit<NonNullArrays<ComputerList>, "computers"> & {
  computers: ComputerData[]
}

const listComputersBound = bind(ListComputers)

/** 读取当前成员已注册的电脑列表。 */
export function listComputers() {
  return listComputersBound() as Promise<ComputerListData>
}

/** 撤销当前成员的电脑。 */
export const revokeComputer = bind(RevokeComputer)

/** 读取本机在当前工作区的电脑注册状态与运行环境。 */
export const currentComputer = bind(CurrentComputer)

type LocalSkillSourceId = Exclude<LocalSkillSource, LocalSkillSource.$zero>

export type LocalSkillData = Omit<NonNullArrays<LocalEnvironment>["skills"][number], "source"> & {
  source: LocalSkillSourceId
}

export type LocalEnvironmentData = Omit<NonNullArrays<LocalEnvironment>, "skills"> & {
  skills: LocalSkillData[]
}

export type LocalMCPServerData = LocalEnvironmentData["mcpServers"][number]

const getLocalEnvironmentBound = bind(GetLocalEnvironment)

/** 读取本机为 AI 员工提供的运行环境、本地 MCP 服务与技能。 */
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

/** 试启动并添加这台电脑上的本地 MCP 服务，同名服务被替换。 */
export const addLocalMCPServer = bind(AddLocalMCPServer)

/** 删除这台电脑上的本地 MCP 服务。 */
export const removeLocalMCPServer = bind(RemoveLocalMCPServer)

/** 从来源把技能安装到这台电脑，同名技能被替换。 */
export const installLocalSkill = bind(InstallLocalSkill)

/** 删除 AI 员工安装在这台电脑上的技能。 */
export const removeLocalSkill = bind(RemoveLocalSkill)

/** 订阅原生端本机电脑状态变化，返回取消订阅函数。 */
export function onLocalComputerChanged(listener: () => void) {
  return Events.On(localComputerChangedEventName, () => listener())
}
