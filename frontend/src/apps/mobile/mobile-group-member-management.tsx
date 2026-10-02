/** 移动端逐个移除成员和转让群主的独立页面：群主可操作全部成员，其他成员只能移出本人负责的个人 AI 员工。 */
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"

import {
  GroupParticipantRole,
  OrganizationIdentityType,
  removeGroupConversationMember,
  transferGroupConversationOwner,
  type GroupParticipant,
} from "@/api"
import type { MobileGroupDetailsContext } from "@/apps/mobile/mobile-group-context"
import { MobileGroupMemberList } from "@/apps/mobile/mobile-group-members"
import { useMobileBack } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useMountedRef } from "@/hooks/use-mounted-ref"

/** 列出可操作成员并二次确认，移除后留在列表，转让后返回群详情。 */
export function MobileGroupMemberActionPage({
  action,
}: {
  action: "remove" | "transfer"
}) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const { group, canManage, archived, busy, onSave } =
    useOutletContext<MobileGroupDetailsContext>()
  const { identity } = useMobileWorkspace()
  const close = useMobileBack(`/chats/group/${group.id}/details`)
  const [target, setTarget] = useState<GroupParticipant | null>(null)
  // 确认框开关独立于目标成员，关闭时保留 target 供标题展示姓名。
  const [confirming, setConfirming] = useState(false)
  const trigger = useRef<HTMLElement | null>(null)
  const alive = useMountedRef()
  const removing = action === "remove"
  const allowed = canManage || (removing && !archived)
  const name = target ? personalAgentDisplayName(target.displayName, target.personalResponsibleName) : ""
  const config = removing
    ? {
        title: t("group.removeMembers"),
        empty: t("group.removeMembersEmpty"),
        notice: t("group.removeArchived"),
        button: t("common:actions.remove"),
        buttonLabel: (memberName: string) =>
          t("group.removeMember", { name: memberName }),
        confirmTitle: t("inbox:groupRemoveMemberTitle", { name }),
        confirmDescription: t("inbox:groupRemoveMemberDescription"),
        destructive: true,
        submit: (identityID: string) =>
          removeGroupConversationMember(group.id, {
            memberIdentityId: identityID,
          }),
      }
    : {
        title: t("inbox:groupTransferOwner"),
        empty: t("group.transferOwnerEmpty"),
        notice: t(
          archived ? "group.transferArchived" : "group.transferOwnerOnly",
        ),
        button: t("group.transfer"),
        buttonLabel: (memberName: string) =>
          t("group.transferOwnerTo", { name: memberName }),
        confirmTitle: t("inbox:groupTransferOwnerTitle", { name }),
        confirmDescription: t("inbox:groupTransferOwnerDescription"),
        destructive: false,
        submit: (identityID: string) =>
          transferGroupConversationOwner(group.id, {
            ownerIdentityId: identityID,
          }),
      }
  // 群主移除候选为群主以外的成员，其他成员只能移出本人负责的个人 AI 员工；转让候选为群主以外的真人成员。
  const candidates = group.participants.filter(
    (member) =>
      member.role !== GroupParticipantRole.GroupParticipantRoleOwner &&
      (removing
        ? canManage || member.personalResponsibleIdentityId === identity.user.identityId
        : member.identityType ===
          OrganizationIdentityType.OrganizationIdentityTypeUser),
  )

  /** 提交确认的成员操作，离开页面后忽略迟到结果。 */
  async function confirm(member: GroupParticipant) {
    const success = await onSave(() => config.submit(member.identityId))
    if (!success || !alive.current) return
    setConfirming(false)
    if (!removing) close()
  }

  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <MobilePageHeader
        title={config.title}
        backTo={`/chats/group/${group.id}/details`}
        backDisabled={busy}
      />
      {allowed ? null : (
        <p
          className="border-b px-4 py-3 text-sm text-muted-foreground"
          role="status"
        >
          {config.notice}
        </p>
      )}
      <MobileGroupMemberList
        members={candidates}
        storageKey={`group-${action}:${group.id}`}
        emptyText={config.empty}
        trailing={(member) => (
          <Button
            type="button"
            variant="outline"
            className="min-h-11 shrink-0"
            disabled={busy || !allowed}
            aria-label={config.buttonLabel(personalAgentDisplayName(member.displayName, member.personalResponsibleName))}
            onClick={(event) => {
              trigger.current = event.currentTarget
              setTarget(member)
              setConfirming(true)
            }}
          >
            {config.button}
          </Button>
        )}
      />
      <ConfirmationDialog
        open={confirming && allowed}
        pending={busy}
        title={config.confirmTitle}
        description={config.confirmDescription}
        destructive={config.destructive}
        onOpenChange={(open) => {
          if (!open) setConfirming(false)
        }}
        onConfirm={() => target && void confirm(target)}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          trigger.current?.focus({ preventScroll: true })
        }}
      />
    </section>
  )
}
