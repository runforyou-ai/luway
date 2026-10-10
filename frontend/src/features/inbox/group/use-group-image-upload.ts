/** 建群表单共用的群图片即时上传。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { FilePurpose } from "@/api"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { recoverSession } from "@/lib/session-navigation"

/** 管理待上传的群图片，失败时恢复会话入口或按 log 动作名记录日志并提示上传失败。 */
export function useGroupImageUpload(log: string) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  return usePendingImageUpload({
    purpose: FilePurpose.GroupImage,
    onError: (error) => {
      if (recoverSession(error, navigate)) return
      console.warn(`${log}失败`, error)
      toast.error(t("groupImageUploadError"))
    },
  })
}
