/** 各端共用的群聊解散确认、提交与查询刷新。 */
import { useTranslation } from "react-i18next"

import { dissolveGroupConversation, type GroupConversation } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useGroupDisplayName } from "@/hooks/use-conversation-name"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 解散成功后保留当前视图，失败时保留确认框供重试。 */
export function GroupDissolveDialog({
  group,
  open,
  onOpenChange,
  trigger,
}: {
  group: GroupConversation
  open: boolean
  onOpenChange: (open: boolean) => void
  trigger?: HTMLElement | null
}) {
  const { t } = useTranslation("inbox")
  const groupName = useGroupDisplayName()
  const reportError = useRequestErrorReporter()
  const save = useImmediateSave()
  const invalidate = useResourceInvalidator()

  /** 提交解散并刷新各端共享的群资料、消息和列表。 */
  async function dissolve() {
    const request = save.begin()
    if (request === null) return
    try {
      await dissolveGroupConversation(group.id)
      await Promise.all([
        invalidate(resourceKeys.groupConversation(group.id)),
        invalidate(resourceKeys.conversationMessages(group.id)),
        invalidate(resourceKeys.inbox()),
      ])
      if (save.isCurrent(request)) onOpenChange(false)
    } catch (error) {
      if (!save.isCurrent(request)) return
      const recovered = reportError(error, {
        log: "解散群聊",
        context: { conversationID: group.id },
        fallback: t("groupDissolveError"),
      })
      if (recovered) return
      void invalidate(resourceKeys.groupConversation(group.id))
    } finally {
      save.finish(request)
    }
  }

  return (
    <ConfirmationDialog
      open={open}
      pending={save.saving}
      title={t("groupDissolveTitle", {
        name: groupName(group, group.participants.length),
      })}
      description={t("groupDissolveDescription")}
      onOpenChange={onOpenChange}
      onConfirm={() => void dissolve()}
      onCloseAutoFocus={
        trigger
          ? (event) => {
              event.preventDefault()
              trigger.focus({ preventScroll: true })
            }
          : undefined
      }
    />
  )
}
