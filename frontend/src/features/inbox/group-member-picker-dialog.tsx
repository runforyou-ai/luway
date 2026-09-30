/** 群聊成员的搜索、多选和批量添加对话框。 */
import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError, OrganizationIdentityType, type GroupParticipant, type MemberOption } from "@/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { GroupMemberPicker } from "@/features/inbox/group-member-picker"
import { listChatTargets } from "@/features/inbox/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

import { groupMemberMaxCount } from "@/features/inbox/group-conversation-schema"

/** 选择尚未加入群聊的企业成员；ownAssistantsOnly 时只列出本人名下的助理。 */
export function GroupMemberPickerDialog({
  open,
  participants,
  ownAssistantsOnly,
  onOpenChange,
  onAdd,
}: {
  open: boolean
  participants: GroupParticipant[]
  ownAssistantsOnly: boolean
  onOpenChange: (open: boolean) => void
  onAdd: (members: MemberOption[]) => Promise<void>
}) {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const [query, setQuery] = useState("")
  const [selectedIdentityIDs, setSelectedIdentityIDs] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const resource = useResource(resourceKeys.chatTargets(), listChatTargets, {
    enabled: open,
    staleTime: 0,
  })
  const participantIdentityIDs = useMemo(
    () =>
      new Set(participants.map((participant) => participant.identityId)),
    [participants],
  )
  const availableMembers = useMemo(
    () =>
      (resource.data ?? []).filter((member) =>
        !participantIdentityIDs.has(member.id) &&
        (!ownAssistantsOnly || member.type === OrganizationIdentityType.OrganizationIdentityTypeAssistant)),
    [ownAssistantsOnly, participantIdentityIDs, resource.data],
  )
  const remainingCount = Math.max(0, groupMemberMaxCount - participants.length)

  /** 关闭时清空尚未提交的成员选择。 */
  function changeOpen(nextOpen: boolean) {
    if (!nextOpen) {
      setQuery("")
      setSelectedIdentityIDs([])
    }
    onOpenChange(nextOpen)
  }

  /** 添加选中的群聊成员。 */
  async function addSelectedMembers() {
    if (selectedIdentityIDs.length === 0) return
    const selectedMembers = availableMembers.filter((member) =>
      selectedIdentityIDs.includes(member.id),
    )
    setSaving(true)
    try {
      await onAdd(selectedMembers)
      changeOpen(false)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("添加群聊成员失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["memberIdentityIds"])
          : t("groupAddMembersError"),
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="max-h-[min(42rem,calc(100svh-2rem))] max-w-2xl overflow-hidden">
        <DialogHeader>
          <DialogTitle>{t("groupAddMembers")}</DialogTitle>
          <DialogDescription>
            {t(ownAssistantsOnly ? "groupAddAssistantsDescription" : "groupAddMembersDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="grid min-h-0 gap-4">
          <GroupMemberPicker
            label={t("groupMemberSearch")}
            emptyMessage={t(ownAssistantsOnly ? "groupAssistantsNoCandidates" : "groupMembersNoCandidates")}
            members={availableMembers}
            selected={selectedIdentityIDs}
            onChange={setSelectedIdentityIDs}
            query={query}
            onQueryChange={setQuery}
            selectionLimit={remainingCount}
            disabled={saving}
            loading={resource.loading}
            error={Boolean(resource.error)}
            onRetry={() => void resource.refresh()}
          />

          <div className="flex items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {t("groupMembersSelected", {
                count: selectedIdentityIDs.length,
              })}
            </span>
            <div className="flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => changeOpen(false)}
              >
                {t("common:actions.cancel")}
              </Button>
              <Button
                type="button"
                disabled={selectedIdentityIDs.length === 0 || saving}
                onClick={() => void addSelectedMembers()}
              >
                {saving ? t("groupAddingMembers") : t("groupAddMembers")}
              </Button>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
