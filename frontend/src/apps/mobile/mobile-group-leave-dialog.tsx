/** 移动端普通成员退出群聊的确认交互。 */
import { useTranslation } from "react-i18next"
import { leaveGroupConversation, type GroupConversationData } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"

/** 普通成员确认退出，成功后由外层返回消息列表。 */
export function MobileGroupLeaveDialog({
  group,
  busy,
  trigger,
  onClose,
  onSave,
}: {
  group: GroupConversationData
  busy: boolean
  trigger: HTMLElement | null
  onClose: () => void
  onSave: (action: () => Promise<unknown>, change: "leave") => Promise<boolean>
}) {
  const { t } = useTranslation("inbox")
  return (
    <ConfirmationDialog
      open
      pending={busy}
      title={t("groupLeaveTitle")}
      description={t("groupLeaveDescription")}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      onConfirm={async () => {
        const success = await onSave(
          () => leaveGroupConversation(group.id),
          "leave",
        )
        if (success) onClose()
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault()
        trigger?.focus({ preventScroll: true })
      }}
    />
  )
}
