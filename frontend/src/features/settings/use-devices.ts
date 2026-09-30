/** 当前用户设备列表的读取与撤销，供各端设备页共用。 */
import { useTranslation } from "react-i18next"

import {
  currentDevice,
  listDevices,
  revokeDevice,
  type DeviceData,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useResource } from "@/hooks/use-resource"

/** 读取已注册设备和本机设备编号，并管理撤销设备的二次确认。 */
export function useDevices() {
  const { t } = useTranslation("settings")
  const resource = useResource(resourceKeys.devices(), () => listDevices(), {
    staleTime: 0,
    refetchOnWindowFocus: true,
  })
  const { data: local } = useResource(
    resourceKeys.currentDevice(),
    () => currentDevice(),
    { staleTime: 0, refetchOnWindowFocus: true },
  )
  const revocation = useConfirmedAction<DeviceData>({
    action: (device) => revokeDevice(device.id),
    invalidateKeys: () => [resourceKeys.devices(), resourceKeys.currentDevice()],
    successMessage: () => t("devices.revoke.success"),
    errorMessage: () => t("devices.revoke.error"),
    logLabel: "撤销设备",
  })
  return {
    resource,
    devices: resource.data?.devices ?? [],
    localDeviceId: local?.deviceId ?? "",
    revocation,
  }
}
