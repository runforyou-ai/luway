/** 服务会话处理周期的回复条件、领取、转交、关闭与重新打开操作。 */
import { useEffect, useState } from "react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  ChannelDelivery,
  WorkspaceIdentityType,
  ServiceSessionStatus,
  ServiceSessionTargetKind,
  ServiceSource,
  claimServiceSession,
  closeServiceSession,
  listServiceAssignees,
  listServiceQueueTeams,
  reopenServiceSession,
  transferServiceSession,
  type ServiceInboxConversationData,
  type ServiceSession,
  type InboxAssignee,
  type ServiceQueueTeam,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator } from "@/components/ui/dropdown-menu"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"

type CustomerSummary = ServiceInboxConversationData["service"]

/** 判断服务会话是否支持处理人回复：渠道来源按渠道外发能力判断，其他来源的发起人直接在会话中读到回复。 */
function customerReplySupported(customer: CustomerSummary) {
  if (customer.source !== ServiceSource.Channel) return true
  const delivery = customer.channel?.capabilities.delivery
  return delivery !== undefined && delivery !== ChannelDelivery.None
}

/** 判断受回复窗口限制的渠道会话在 now 时是否没有可用的回复窗口。 */
export function customerReplyWindowClosed(customer: CustomerSummary, now: number) {
  if (!customer.channel?.capabilities.replyWindow) return false
  const window = customer.replyWindow
  return !window || Date.parse(window.expiresAt) <= now || window.remaining === 0
}

/** 返回当前时刻，在会话回复窗口到期时刷新。 */
export function useReplyWindowNow(customer: CustomerSummary | null) {
  const expiresAt = customer?.replyWindow?.expiresAt
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!expiresAt) return
    const delay = Date.parse(expiresAt) - Date.now()
    // 已到期时立即刷新，未到期时在到期后刷新；定时器上限约 24 天。
    const timer = window.setTimeout(() => setNow(Date.now()), Math.min(Math.max(delay, 0) + 100, 2_147_483_647))
    return () => window.clearTimeout(timer)
  }, [expiresAt])
  return now
}

/** 按渠道能力、接待资格、处理状态、回复窗口和负责人返回当前成员在 now 时不能对客回复的原因。 */
export function customerReplyDisabledReason(
  customer: CustomerSummary,
  currentIdentityId: string,
  handlesServiceRequests: boolean,
  now: number,
  t: TFunction<"inbox">,
) {
  switch (customerReplyBlocker(customer, currentIdentityId, handlesServiceRequests, now)) {
    case "channel":
      return t("channelReplyUnsupported")
    case "handling":
      return t("replyHandlingUnavailable")
    case "closed":
      return t("replyClosedUnavailable")
    case "window":
      return t("replyWindowClosedUnavailable")
    case "assigned":
      return t("replyAssignedUnavailable", { name: customer.assignee?.displayName ?? "" })
    default:
      return null
  }
}

/** 返回当前成员在 now 时不能对客回复的原因类型：渠道不支持、未开启接待、周期已关闭、回复窗口已关闭或由他人负责，可以回复时为 null。 */
export function customerReplyBlocker(
  customer: CustomerSummary,
  currentIdentityId: string,
  handlesServiceRequests: boolean,
  now: number,
) {
  if (!customerReplySupported(customer)) return "channel"
  if (!handlesServiceRequests) return "handling"
  if (customer.serviceSessionStatus === ServiceSessionStatus.Closed) return "closed"
  if (customerReplyWindowClosed(customer, now)) return "window"
  if (customer.assignee && customer.assignee.identityId !== currentIdentityId) return "assigned"
  return null
}

/** 管理客服处理周期命令的执行状态、可用操作和关闭确认；只有开启接待的成员可以领取、转交、关闭与重开。 */
export function useCustomerSessionActions(
  conversation: ServiceInboxConversationData | null,
  currentIdentityId: string,
  handlesServiceRequests: boolean,
  onChanged: (session: ServiceSession) => void,
) {
  const { t } = useTranslation("inbox")
  const reportError = useRequestErrorReporter()
  const [operation, setOperation] = useState("")
  const [closeConfirmationOpen, setCloseConfirmationOpen] = useState(false)
  const customer = conversation?.service ?? null
  const sessionOpen =
    customer?.serviceSessionStatus === ServiceSessionStatus.Open
  const sessionClosed =
    customer?.serviceSessionStatus === ServiceSessionStatus.Closed
  const assignedToCurrentUser = customer?.assignee?.identityId === currentIdentityId
  const { data: assignees = [] } = useResource(
    resourceKeys.serviceAssignees(),
    () => listServiceAssignees(),
    { enabled: Boolean(customer && sessionOpen && assignedToCurrentUser) },
  )
  const { data: transferTeams = [] } = useResource(
    resourceKeys.serviceQueueTeams(),
    () => listServiceQueueTeams(),
    { enabled: Boolean(customer && sessionOpen && assignedToCurrentUser) },
  )
  // 支持回复的渠道会话可转给服务同一对象的 AI 员工，其他渠道只转给真人；其他来源只能交还接待发起人的 AI 员工。
  const channelSource = customer?.source === ServiceSource.Channel
  const people = assignees.filter(
    (assignee) =>
      assignee.identityId !== currentIdentityId &&
      (assignee.type !== WorkspaceIdentityType.Agent ||
        (channelSource &&
          customerReplySupported(customer) &&
          assignee.serviceAudiences.includes(customer.audience))),
  )
  const transferCandidates: InboxAssignee[] =
    !channelSource && customer?.agentIdentityId
      ? [
          ...people,
          {
            identityId: customer.agentIdentityId,
            type: WorkspaceIdentityType.Agent,
            displayName: customer.agentName ?? "",
            avatarUrl: "",
            serviceAudiences: [],
          },
        ]
      : people

  /** 执行客服处理周期命令，并把命令后的处理周期交给上层刷新受影响视图。 */
  async function run(
    nextOperation: string,
    execute: (conversationID: string) => Promise<ServiceSession>,
    successMessage: string,
  ) {
    if (!conversation) return
    setOperation(nextOperation)
    try {
      onChanged(await execute(conversation.id))
      toast.success(successMessage)
    } catch (error) {
      reportError(error, {
        log: "更新客服会话",
        context: { conversationId: conversation.id, operation: nextOperation },
        fallback: t("conversationActionError"),
      })
    } finally {
      setOperation("")
    }
  }

  return {
    operation,
    sessionOpen,
    sessionClosed,
    assignedToCurrentUser,
    reopenable: sessionClosed && handlesServiceRequests,
    claimable: sessionOpen && !assignedToCurrentUser && handlesServiceRequests,
    transferable: sessionOpen && assignedToCurrentUser && handlesServiceRequests,
    // 未分配或由本人负责的开放会话可以关闭。
    closable:
      sessionOpen && (!customer?.assignee || assignedToCurrentUser) && handlesServiceRequests,
    transferCandidates,
    transferTeams,
    closeConfirmationOpen,
    setCloseConfirmationOpen,
    unansweredMentionCount: customer?.unansweredMentionCount ?? 0,
    reopen: () =>
      run("reopen", reopenServiceSession, t("conversationReopenSuccess")),
    claim: () =>
      run(
        "claim",
        claimServiceSession,
        customer?.assignee
          ? t("conversationTakeoverSuccess")
          : t("conversationClaimSuccess"),
      ),
    transferToMember: (assignee: InboxAssignee) =>
      run(
        `transfer:${assignee.identityId}`,
        (conversationID) =>
          transferServiceSession(conversationID, {
            kind: ServiceSessionTargetKind.ServiceSessionTargetMember,
            identityId: assignee.identityId,
          }),
        t("conversationTransferSuccess", { name: assignee.displayName }),
      ),
    transferToTeam: (team: ServiceQueueTeam) =>
      run(
        `transfer:${team.id}`,
        (conversationID) =>
          transferServiceSession(conversationID, {
            kind: ServiceSessionTargetKind.ServiceSessionTargetTeam,
            teamId: team.id,
          }),
        t("conversationTransferSuccess", { name: team.name }),
      ),
    transferToPublicQueue: () =>
      run(
        "transfer:public-queue",
        (conversationID) =>
          transferServiceSession(conversationID, {
            kind: ServiceSessionTargetKind.ServiceSessionTargetPublicQueue,
          }),
        t("conversationTransferQueueSuccess"),
      ),
    close: () =>
      run("close", closeServiceSession, t("conversationCloseSuccess")),
  }
}

export type CustomerSessionActions = ReturnType<typeof useCustomerSessionActions>

/** 转交去向菜单项：同事、团队队列和公共队列分组展示。 */
export function CustomerTransferMenuItems({
  actions,
  itemClassName,
}: {
  actions: CustomerSessionActions
  itemClassName?: string
}) {
  const { t } = useTranslation("inbox")
  return (
    <>
      {actions.transferCandidates.length > 0 ? (
        <>
          <DropdownMenuLabel>{t("conversationTransferCoworkers")}</DropdownMenuLabel>
          {actions.transferCandidates.map((assignee) => (
            <DropdownMenuItem
              key={assignee.identityId}
              className={itemClassName}
              onSelect={() => void actions.transferToMember(assignee)}
            >
              {assignee.displayName}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
        </>
      ) : null}
      {actions.transferTeams.length > 0 ? (
        <>
          <DropdownMenuLabel>{t("conversationTransferTeams")}</DropdownMenuLabel>
          {actions.transferTeams.map((team) => (
            <DropdownMenuItem
              key={team.id}
              className={itemClassName}
              disabled={!team.available}
              onSelect={() => void actions.transferToTeam(team)}
            >
              <span className="min-w-0 flex-1 truncate">{team.name}</span>
              {team.available ? null : (
                <span className="text-xs text-muted-foreground">
                  {t("conversationTransferTeamUnavailable")}
                </span>
              )}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
        </>
      ) : null}
      <DropdownMenuItem
        className={itemClassName}
        onSelect={() => void actions.transferToPublicQueue()}
      >
        {t("conversationTransferPublicQueue")}
      </DropdownMenuItem>
    </>
  )
}

/** 关闭客户会话处理周期前的确认弹窗。 */
export function CustomerSessionCloseDialog({
  actions,
}: {
  actions: CustomerSessionActions
}) {
  const { t } = useTranslation("inbox")
  return (
    <ConfirmationDialog
      open={actions.closeConfirmationOpen}
      pending={false}
      title={t("conversationCloseConfirmTitle")}
      description={
        <>
          {t("conversationCloseConfirmDescription")}
          {actions.unansweredMentionCount > 0 ? (
            <span className="mt-1 block text-foreground">
              {t("conversationCloseUnansweredMentions", { count: actions.unansweredMentionCount })}
            </span>
          ) : null}
        </>
      }
      onOpenChange={actions.setCloseConfirmationOpen}
      onConfirm={() => {
        actions.setCloseConfirmationOpen(false)
        void actions.close()
      }}
    />
  )
}
