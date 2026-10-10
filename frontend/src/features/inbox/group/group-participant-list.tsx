/** 群聊侧边面板中的成员列表和成员管理交互。 */
import { useId, useMemo, useRef, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import {
  CrownIcon,
  MoreHorizontalIcon,
  SearchIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  GroupParticipantRole,
  WorkspaceIdentityType,
  type GroupParticipant,
  type MemberOption,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { GroupMemberPickerDialog } from "@/features/inbox/group/group-member-picker-dialog"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useRequestErrorReporter, type RequestErrorOptions } from "@/hooks/use-request-error-reporter"
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
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const reportError = useRequestErrorReporter()
  const memberSearchID = useId()
  const [query, setQuery] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const [transferring, setTransferring] =
    useState<GroupParticipant | null>(null)
  const [removing, setRemoving] = useState<GroupParticipant | null>(null)
  const [leaving, setLeaving] = useState<GroupParticipant | null>(null)
  // 转让、移出与退出共用同一进行中状态，失败时按操作提示。
  const action = useMutation({
    mutationFn: ({ run }: { run: () => Promise<void>; failure: RequestErrorOptions }) => run(),
    onError: (error, { failure }) => reportError(error, failure),
  })
  const acting = action.isPending
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
          personalAgentDisplayName(participant.displayName, participant.personalResponsibleName)
            .toLocaleLowerCase()
            .includes(normalizedQuery),
      ),
    [personalAgentDisplayName, normalizedQuery, participants],
  )
  /** 转让群主并关闭确认框。 */
  function transferOwner() {
    if (!transferring) return
    const identityID = transferring.identityId
    action.mutate({
      run: () => onTransferOwner(identityID),
      failure: { log: "转让群主", fallback: t("groupTransferOwnerError"), fields: ["ownerIdentityId"] },
    }, { onSuccess: () => setTransferring(null) })
  }

  /** 移除成员并关闭确认框。 */
  function removeMember() {
    if (!removing) return
    const identityID = removing.identityId
    action.mutate({
      run: () => onRemove(identityID),
      failure: { log: "移出群聊成员", fallback: t("groupRemoveMemberError"), fields: ["memberIdentityId"] },
    }, { onSuccess: () => setRemoving(null) })
  }

  /** 普通成员退出群聊并关闭确认框。 */
  function leaveGroup() {
    if (!leaving) return
    action.mutate({
      run: onLeave,
      failure: { log: "退出群聊", fallback: t("groupLeaveError") },
    }, { onSuccess: () => setLeaving(null) })
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
        {/* 群主添加任意成员，其他成员添加本人负责的个人 AI 员工。 */}
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
                GroupParticipantRole.Owner
              const ownPersonalAgent =
                participant.personalResponsibleIdentityId === currentIdentityID
              const showActions =
                !readOnly && !isOwner && (isCurrent || canManage || ownPersonalAgent)
              return (
                <div
                  key={participant.identityId}
                  className="flex items-center gap-3 rounded-md px-2 py-2"
                >
                  <GroupParticipantAvatar participant={participant} />
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {personalAgentDisplayName(participant.displayName, participant.personalResponsibleName)}
                    {isCurrent ? (
                      <span className="ml-1 text-xs text-muted-foreground">
                        {t("groupMemberYou")}
                      </span>
                    ) : null}
                  </span>
                  {participant.identityType === WorkspaceIdentityType.Agent ? (
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
                              name: personalAgentDisplayName(participant.displayName, participant.personalResponsibleName),
                            })}
                            title={t("groupMemberMore", {
                              name: personalAgentDisplayName(participant.displayName, participant.personalResponsibleName),
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
                              {participant.identityType === WorkspaceIdentityType.User ? (
                                <DropdownMenuItem
                                  onSelect={() => setTransferring(participant)}
                                >
                                  {t("groupTransferOwner")}
                                </DropdownMenuItem>
                              ) : null}
                              {participant.identityType === WorkspaceIdentityType.User ? (
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
        ownPersonalAgentsOnly={!canManage}
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
        onConfirm={transferOwner}
        onCloseAutoFocus={restoreActionFocus}
      />

      <ConfirmationDialog
        open={removing !== null}
        pending={acting}
        title={t("groupRemoveMemberTitle", {
          name: removing ? personalAgentDisplayName(removing.displayName, removing.personalResponsibleName) : "",
        })}
        description={t("groupRemoveMemberDescription")}
        onOpenChange={(open) => !open && setRemoving(null)}
        onConfirm={removeMember}
        onCloseAutoFocus={restoreActionFocus}
      />

      <ConfirmationDialog
        open={leaving !== null && !readOnly && !canManage}
        pending={acting}
        title={t("groupLeaveTitle")}
        description={t("groupLeaveDescription")}
        onOpenChange={(open) => !open && setLeaving(null)}
        onConfirm={leaveGroup}
        onCloseAutoFocus={restoreActionFocus}
      />
    </div>
  )
}
