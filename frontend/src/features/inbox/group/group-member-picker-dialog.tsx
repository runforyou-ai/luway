/** 群聊成员的搜索、多选和批量添加对话框。 */
import { useMemo, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import type { GroupParticipant, MemberOption } from "@/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { GroupMemberPicker } from "@/features/inbox/group/group-member-picker"
import { listChatTargets } from "@/features/inbox/shared/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"

import { groupMemberMaxCount } from "@/features/inbox/group/group-conversation-schema"

/** 选择尚未加入群聊的企业成员；ownPersonalAgentsOnly 时只列出本人负责的个人 AI 员工。 */
export function GroupMemberPickerDialog({
  open,
  participants,
  ownPersonalAgentsOnly,
  onOpenChange,
  onAdd,
}: {
  open: boolean
  participants: GroupParticipant[]
  ownPersonalAgentsOnly: boolean
  onOpenChange: (open: boolean) => void
  onAdd: (members: MemberOption[]) => Promise<void>
}) {
  const { t } = useTranslation(["inbox", "common"])
  const reportError = useRequestErrorReporter()
  const [query, setQuery] = useState("")
  const [selectedIdentityIDs, setSelectedIdentityIDs] = useState<string[]>([])
  const addition = useMutation({
    mutationFn: (members: MemberOption[]) => onAdd(members),
    onError: (error) => reportError(error, {
      log: "添加群聊成员",
      fallback: t("groupAddMembersError"),
      fields: ["memberIdentityIds"],
    }),
  })
  const saving = addition.isPending
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
        (!ownPersonalAgentsOnly || member.personal)),
    [ownPersonalAgentsOnly, participantIdentityIDs, resource.data],
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
  function addSelectedMembers() {
    if (selectedIdentityIDs.length === 0) return
    const selectedMembers = availableMembers.filter((member) =>
      selectedIdentityIDs.includes(member.id),
    )
    addition.mutate(selectedMembers, { onSuccess: () => changeOpen(false) })
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="max-h-[min(42rem,calc(100svh-2rem))] max-w-2xl overflow-hidden">
        <DialogHeader>
          <DialogTitle>{t("groupAddMembers")}</DialogTitle>
          <DialogDescription>
            {t(ownPersonalAgentsOnly ? "groupAddPersonalAgentsDescription" : "groupAddMembersDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="grid min-h-0 gap-4">
          <GroupMemberPicker
            label={t("groupMemberSearch")}
            emptyMessage={t(ownPersonalAgentsOnly ? "groupPersonalAgentsNoCandidates" : "groupMembersNoCandidates")}
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
                onClick={addSelectedMembers}
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
