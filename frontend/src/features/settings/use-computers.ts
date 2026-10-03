/** 当前成员电脑列表的读取与撤销，供各端电脑页共用。 */
import { useTranslation } from "react-i18next"

import {
  currentComputer,
  listComputers,
  revokeComputer,
  type ComputerData,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useResource } from "@/hooks/use-resource"

/** 读取已注册电脑和本机电脑编号，并管理撤销电脑的二次确认。 */
export function useComputers() {
  const { t } = useTranslation("settings")
  const resource = useResource(resourceKeys.computers(), () => listComputers(), {
    staleTime: 0,
    refetchOnWindowFocus: true,
  })
  const { data: local } = useResource(
    resourceKeys.currentComputer(),
    () => currentComputer(),
    { staleTime: 0, refetchOnWindowFocus: true },
  )
  const revocation = useConfirmedAction<ComputerData>({
    action: (computer) => revokeComputer(computer.id),
    invalidateKeys: () => [resourceKeys.computers(), resourceKeys.currentComputer()],
    successMessage: () => t("computers.revoke.success"),
    errorMessage: () => t("computers.revoke.error"),
    logLabel: "撤销电脑",
  })
  return {
    resource,
    computers: resource.data?.computers ?? [],
    localComputerId: local?.computerId ?? "",
    revocation,
  }
}

/** 返回电脑的在线说明：在线、最近在线时间或尚未连接。 */
export function useComputerPresence() {
  const { t } = useTranslation("settings")
  return (computer: ComputerData, formatDateTime: (value: string) => string) => {
    if (computer.online) {
      return t("computers.list.online")
    }
    if (computer.lastSeenAt) {
      return t("computers.list.lastSeen", { time: formatDateTime(computer.lastSeenAt) })
    }
    return t("computers.list.neverConnected")
  }
}
