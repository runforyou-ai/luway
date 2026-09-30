/** 群聊侧边面板中的成员列表和成员管理交互。 */
import { useId, useMemo, useRef, useState } from "react"
import {
  CrownIcon,
  MoreHorizontalIcon,
  SearchIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  GroupParticipantRole,
  OrganizationIdentityType,
  isApiError,
  type GroupParticipant,
  type MemberOption,
} from "@/api"
import { ProfileAvatar } from "@/components/profile-avatar"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { useAssistantDisplayName } from "@/hooks/use-assistant-display-name"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { GroupMemberPickerDialog } from "@/features/inbox/group-member-picker-dialog"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { isAIIdentityType } from "@/lib/identity-type"

/** 展示群聊成员头像。 */
function GroupParticipantAvatar({
  participant,
}: {
  participant: GroupParticipant
}) {
  return (
    <ProfileAvatar
      imageURL={participant.avatarUrl}
      name={participant.displayName}
      fallback={isAIIdentityType(participant.identityType) ? "agent" : "person"}
      className="size-9"
    />
  )
}

/** 展示当前成员并提供群主和本人可用的成员操作。 */
export function GroupParticipantList({
  participants,
  currentIdentityID,
  canManage,
  readOnly,
  onAdd,
  onTransferOwner,
  onRemove,
  onLeave,
}: {
  participants: GroupParticipant[]
  currentIdentityID: string
  canManage: boolean
  readOnly: boolean
  onAdd: (members: MemberOption[]) => Promise<void>
  onTransferOwner: (identityID: string) => Promise<void>
  onRemove: (identityID: string) => Promise<void>
  onLeave: () => Promise<void>
}) {
  const { t } = useTranslation(["inbox", "common"])
  const assistantDisplayName = useAssistantDisplayName()
  const navigate = useNavigate()
  const memberSearchID = useId()
  const [query, setQuery] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const [transferring, setTransferring] =
    useState<GroupParticipant | null>(null)
  const [removing, setRemoving] = useState<GroupParticipant | null>(null)
  const [leaving, setLeaving] = useState<GroupParticipant | null>(null)
  const [acting, setActing] = useState(false)
  // 菜单项随菜单卸载，确认框关闭后把焦点还给打开菜单的按钮。
  const actionTrigger = useRef<HTMLElement | null>(null)

  /** 阻止菜单关闭时的默认焦点恢复，把焦点还给打开菜单的按钮。 */
  function restoreActionFocus(event: Event) {
    event.preventDefault()
    actionTrigger.current?.focus({ preventScroll: true })
  }
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const visibleParticipants = useMemo(
    () =>
      participants.filter(
        (participant) =>
          !normalizedQuery ||
          assistantDisplayName(participant.displayName, participant.assistantOwnerName)
            .toLocaleLowerCase()
            .includes(normalizedQuery),
      ),
    [assistantDisplayName, normalizedQuery, participants],
  )
  /** 转让群主并关闭确认框。 */
  async function transferOwner() {
    if (!transferring) return
    setActing(true)
    try {
      await onTransferOwner(transferring.identityId)
      setTransferring(null)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("转让群主失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["ownerIdentityId"])
          : t("groupTransferOwnerError"),
      )
    } finally {
      setActing(false)
    }
  }

  /** 移除成员并关闭确认框。 */
  async function removeMember() {
    if (!removing) return
    setActing(true)
    try {
      await onRemove(removing.identityId)
      setRemoving(null)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("移出群聊成员失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["memberIdentityId"])
          : t("groupRemoveMemberError"),
      )
    } finally {
      setActing(false)
    }
  }

  /** 普通成员退出群聊并关闭确认框。 */
  async function leaveGroup() {
    if (!leaving) return
    setActing(true)
    try {
      await onLeave()
      setLeaving(null)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("退出群聊失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error)
          : t("groupLeaveError"),
      )
    } finally {
      setActing(false)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-end gap-2 border-b px-3 py-3">
        <div className="min-w-0 flex-1">
          <label
            htmlFor={memberSearchID}
            className="sr-only"
          >
            {t("groupMemberSearch")}
          </label>
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              id={memberSearchID}
              value={query}
              autoComplete="off"
              className="pl-9"
              aria-label={t("groupMemberSearch")}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
        </div>
        {/* 群主添加任意成员，其他成员添加本人名下的助理。 */}
        {!readOnly ? (
          <Button type="button" size="sm" onClick={() => setAddOpen(true)}>
            {t("groupAddMembers")}
          </Button>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-1.5">
        {visibleParticipants.length === 0 ? (
          <p className="px-6 py-12 text-center text-sm text-muted-foreground">
            {t("membersNoMatches")}
          </p>
        ) : (
          <div className="grid">
            {visibleParticipants.map((participant) => {
              const isCurrent =
                participant.identityId === currentIdentityID
              const isOwner =
                participant.role ===
                GroupParticipantRole.GroupParticipantRoleOwner
              const ownAssistant =
                participant.assistantOwnerIdentityId === currentIdentityID
              const showActions =
                !readOnly && !isOwner && (isCurrent || canManage || ownAssistant)
              return (
                <div
                  key={participant.identityId}
                  className="flex items-center gap-3 rounded-md px-2 py-2"
                >
                  <GroupParticipantAvatar participant={participant} />
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {assistantDisplayName(participant.displayName, participant.assistantOwnerName)}
                    {isCurrent ? (
                      <span className="ml-1 text-xs text-muted-foreground">
                        {t("groupMemberYou")}
                      </span>
                    ) : null}
                  </span>
                  {participant.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent ? (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {t("groupAgent")}
                    </span>
                  ) : null}
                  {isOwner ? (
                    <span className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                      <CrownIcon className="size-3.5" />
                      {t("groupOwner")}
                    </span>
                  ) : null}
                  <span className="flex size-8 shrink-0 items-center justify-center">
                    {showActions ? (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            onPointerDown={(event) => {
                              actionTrigger.current = event.currentTarget
                            }}
                            onKeyDown={(event) => {
                              actionTrigger.current = event.currentTarget
                            }}
                            aria-label={t("groupMemberMore", {
                              name: assistantDisplayName(participant.displayName, participant.assistantOwnerName),
                            })}
                            title={t("groupMemberMore", {
                              name: assistantDisplayName(participant.displayName, participant.assistantOwnerName),
                            })}
                          >
                            <MoreHorizontalIcon />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {isCurrent ? (
                            <DropdownMenuItem
                              destructive
                              onSelect={() => setLeaving(participant)}
                            >
                              {t("groupLeave")}
                            </DropdownMenuItem>
                          ) : (
                            <>
                              {participant.identityType === OrganizationIdentityType.OrganizationIdentityTypeUser ? (
                                <DropdownMenuItem
                                  onSelect={() => setTransferring(participant)}
                                >
                                  {t("groupTransferOwner")}
                                </DropdownMenuItem>
                              ) : null}
                              {participant.identityType === OrganizationIdentityType.OrganizationIdentityTypeUser ? (
                                <DropdownMenuSeparator />
                              ) : null}
                              <DropdownMenuItem
                                destructive
                                onSelect={() => setRemoving(participant)}
                              >
                                {t("groupRemoveMember")}
                              </DropdownMenuItem>
                            </>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    ) : null}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </div>

      <GroupMemberPickerDialog
        open={addOpen}
        participants={participants}
        ownAssistantsOnly={!canManage}
        onOpenChange={setAddOpen}
        onAdd={onAdd}
      />

      <ConfirmationDialog
        open={transferring !== null}
        pending={acting}
        title={t("groupTransferOwnerTitle", {
          name: transferring?.displayName ?? "",
        })}
        description={t("groupTransferOwnerDescription")}
        destructive={false}
        onOpenChange={(open) => !open && setTransferring(null)}
        onConfirm={() => void transferOwner()}
        onCloseAutoFocus={restoreActionFocus}
      />

      <ConfirmationDialog
        open={removing !== null}
        pending={acting}
        title={t("groupRemoveMemberTitle", {
          name: removing ? assistantDisplayName(removing.displayName, removing.assistantOwnerName) : "",
        })}
        description={t("groupRemoveMemberDescription")}
        onOpenChange={(open) => !open && setRemoving(null)}
        onConfirm={() => void removeMember()}
        onCloseAutoFocus={restoreActionFocus}
      />

      <ConfirmationDialog
        open={leaving !== null && !readOnly && !canManage}
        pending={acting}
        title={t("groupLeaveTitle")}
        description={t("groupLeaveDescription")}
        onOpenChange={(open) => !open && setLeaving(null)}
        onConfirm={() => void leaveGroup()}
        onCloseAutoFocus={restoreActionFocus}
      />
    </div>
  )
}
