/** 移动端群成员预览、群资料和个人设置的连续详情页。 */
import { groupMemberMaxCount } from "@/features/inbox/group/group-conversation-schema"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Outlet, useMatch, useNavigate } from "react-router"
import { toast } from "sonner"
import {
  ConversationStatus,
  GroupParticipantRole,
  isNotFoundApiError,
} from "@/api"
import { useMobileGroup } from "@/apps/mobile/groups/mobile-group-context"
import { mobileSearchPath } from "@/apps/mobile/shared/mobile-navigation"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { MobileCoveredPage, MobilePageHeader, MobileScrollArea } from "@/apps/mobile/shared/mobile-page"
import { MobileGroupInfo } from "@/apps/mobile/groups/mobile-group-info"
import { MobileGroupMembersPreview } from "@/apps/mobile/groups/mobile-group-members"
import type { MobileGroupDetailsContext } from "@/apps/mobile/groups/mobile-group-context"
import { GroupDissolveDialog } from "@/features/inbox/group/group-dissolve-dialog"
import { useConversationArchive } from "@/features/inbox/list/use-conversation-archive"
import { useGroupMute } from "@/features/inbox/group/use-group-mute"
import { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
import { MobileGroupLeaveDialog } from "@/apps/mobile/groups/mobile-group-leave-dialog"
import { Button } from "@/components/ui/button"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"

/** 协调群管理操作，成功后刷新服务端事实并保留详情浏览位置。 */
export function MobileGroupDetailsPage() {
  const { t } = useTranslation("mobile")
  const {
    group,
    returnDepth,
    error,
    refreshing,
    refresh,
    onUnavailable,
    onLeft,
    onLeavingChange,
  } = useMobileGroup()
  const { identity } = useMobileWorkspace()
  const navigate = useNavigate()
  const childOpen = !useMatch("/chats/group/:conversationID/details")
  const invalidate = useResourceInvalidator()
  const save = useImmediateSave()
  const reportError = useRequestErrorReporter()
  const mute = useGroupMute(group, onUnavailable)
  const [leaveOpen, setLeaveOpen] = useState(false)
  const [dissolveOpen, setDissolveOpen] = useState(false)
  const trigger = useRef<HTMLElement | null>(null)
  const archive = useConversationArchive()
  // 群资料不含置顶状态，从会话摘要读取，未读到时按未知处理。
  const summary = useConversationSummary(group.id)
  const archived =
    group.status === ConversationStatus.Archived
  const isOwner = group.participants.some(
    (member) =>
      member.identityId === identity.user.identityId &&
      member.role === GroupParticipantRole.Owner,
  )
  const canManage = isOwner && !archived
  // 当前成员负责的已在群内的个人 AI 员工，由本人移出。
  const ownsPersonalAgentInGroup = group.participants.some(
    (member) => member.personalResponsibleIdentityId === identity.user.identityId,
  )
  useEffect(() => {
    // 群聊解散或群主身份变化后关闭退出、解散确认。
    if ((archived || isOwner) && !save.saving) setLeaveOpen(false)
    if (archived || !isOwner) setDissolveOpen(false)
  }, [archived, isOwner, save.saving])

  /** 保存资料或退出群聊，卸载后忽略提示与导航结果。 */
  async function perform(
    action: () => Promise<unknown>,
    change: "messages" | "leave" = "messages",
  ) {
    const request = save.begin()
    if (request === null) return false
    const leaving = change === "leave"
    let completed = false
    if (leaving) onLeavingChange(true)
    try {
      await action()
      completed = true
      await Promise.all([
        invalidate(resourceKeys.groupConversation(group.id), {
          refetchType: leaving ? "none" : "active",
        }),
        invalidate(resourceKeys.inbox(), {
          refetchType: leaving ? "all" : "active",
        }),
        ...(change === "messages"
          ? [invalidate(resourceKeys.conversationMessages(group.id))]
          : []),
      ])
      if (leaving && save.isCurrent(request)) onLeft()
      return save.isCurrent(request)
    } catch (error) {
      if (!save.isCurrent(request)) return false
      if (isNotFoundApiError(error)) {
        if (!recoverSession(error, navigate)) onUnavailable()
      } else if (
        !reportError(error, {
          log: "移动端群管理操作",
          context: { conversationID: group.id },
          fallback: t("group.saveError"),
        })
      )
        void refresh()
      return false
    } finally {
      if (leaving && !completed) onLeavingChange(false)
      save.finish(request)
    }
  }

  return (
    <MobileCoveredPage
      covered={childOpen}
      outlet={
        <Outlet
          context={
            {
              group,
              returnDepth,
              canManage,
              archived,
              busy: save.saving,
              onSave: perform,
            } satisfies MobileGroupDetailsContext
          }
        />
      }
    >
      <MobilePageHeader
        title={t("group.details")}
        backTo={childOpen ? undefined : `/chats/group/${group.id}`}
        actions={
          error ? (
            <Button
              variant="ghost"
              className="min-h-11 text-destructive hover:text-destructive"
              disabled={refreshing}
              onClick={() => void refresh()}
            >
              {t("refreshFailed")}
            </Button>
          ) : null
        }
      />
      <MobileScrollArea storageKey={`group-details:${group.id}`}>
        <MobileGroupMembersPreview
          group={group}
          showRemove={isOwner || ownsPersonalAgentInGroup}
          returnDepth={returnDepth}
          canAdd={
            !archived && !save.saving && group.participants.length < groupMemberMaxCount
          }
          canRemove={
            !save.saving &&
            (canManage ? group.participants.length > 1 : !archived && ownsPersonalAgentInGroup)
          }
        />
        <MobileGroupInfo
          group={group}
          isOwner={isOwner}
          archived={archived}
          busy={save.saving}
          muted={mute.muted}
          muteBusy={mute.saving}
          onEdit={(field) => {
            if (!canManage) {
              toast.message(t(archived ? "group.editArchived" : "group.editOwnerOnly"))
              return
            }
            void navigate(`edit/${field}`, {
              replace: returnDepth === 0,
              state: {
                mobileBack: returnDepth > 0,
                groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
              },
            })
          }}
          onTransfer={() =>
            void navigate("transfer-owner", {
              replace: returnDepth === 0,
              state: {
                mobileBack: returnDepth > 0,
                groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
              },
            })
          }
          onSearch={() =>
            void navigate(mobileSearchPath(group.id), {
              state: { mobileBack: true },
            })
          }
          onFiles={() =>
            void navigate("files", {
              replace: returnDepth === 0,
              state: {
                mobileBack: returnDepth > 0,
                groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
              },
            })
          }
          onLeave={(source) => {
            trigger.current = source
            if (isOwner) setDissolveOpen(true)
            else setLeaveOpen(true)
          }}
          onMute={(muted) => void mute.change(muted)}
          chatArchived={group.archivedAt !== null}
          archiveBusy={archive.saving}
          onArchive={() => void archive.save(group.id, group.archivedAt === null, summary.data?.pinned ?? null)}
        />
      </MobileScrollArea>
      <GroupDissolveDialog
        group={group}
        open={dissolveOpen}
        onOpenChange={setDissolveOpen}
        trigger={trigger.current}
      />
      {leaveOpen && !archived && !isOwner ? (
        <MobileGroupLeaveDialog
          group={group}
          busy={save.saving || mute.saving}
          trigger={trigger.current}
          onClose={() => setLeaveOpen(false)}
          onSave={perform}
        />
      ) : null}
    </MobileCoveredPage>
  )
}
