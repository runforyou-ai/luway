/** 服务会话处理周期的回复条件、领取、转交、关闭与重新打开操作。 */
import { useState } from "react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  ChannelType,
  OrganizationIdentityType,
  ServiceSessionStatus,
  ServiceSessionTargetKind,
  ServiceSource,
  claimServiceSession,
  closeServiceSession,
  isApiError,
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
import {
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

type CustomerSummary = ServiceInboxConversationData["service"]

/** 判断服务会话是否支持处理人回复：渠道来源按渠道外发能力判断，其他来源的发起人直接在会话中读到回复。 */
function customerReplySupported(customer: CustomerSummary) {
  if (customer.source !== ServiceSource.ServiceSourceChannel) return true
  return (
    customer.channel?.type === ChannelType.ChannelTypeWebsite ||
    customer.channel?.type === ChannelType.ChannelTypeTelegram
  )
}

/** 按渠道能力、接待资格、处理状态和负责人返回当前成员不能对客回复的原因。 */
export function customerReplyDisabledReason(
  customer: CustomerSummary,
  currentIdentityId: string,
  handlesServiceRequests: boolean,
  t: TFunction<"inbox">,
) {
  switch (customerReplyBlocker(customer, currentIdentityId, handlesServiceRequests)) {
    case "channel":
      return t("channelReplyUnsupported")
    case "handling":
      return t("replyHandlingUnavailable")
    case "closed":
      return t("replyClosedUnavailable")
    case "assigned":
      return t("replyAssignedUnavailable", { name: customer.assignee?.displayName ?? "" })
    default:
      return null
  }
}

/** 返回当前成员不能对客回复的原因类型：渠道不支持、未开启接待、周期已关闭或由他人负责，可以回复时为 null。 */
export function customerReplyBlocker(
  customer: CustomerSummary,
  currentIdentityId: string,
  handlesServiceRequests: boolean,
) {
  if (!customerReplySupported(customer)) return "channel"
  if (!handlesServiceRequests) return "handling"
  if (customer.serviceSessionStatus === ServiceSessionStatus.ServiceSessionStatusClosed) return "closed"
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
  const navigate = useNavigate()
  const [operation, setOperation] = useState("")
  const [closeConfirmationOpen, setCloseConfirmationOpen] = useState(false)
  const customer = conversation?.service ?? null
  const sessionOpen =
    customer?.serviceSessionStatus === ServiceSessionStatus.ServiceSessionStatusOpen
  const sessionClosed =
    customer?.serviceSessionStatus === ServiceSessionStatus.ServiceSessionStatusClosed
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
  // 网站和 Telegram 会话可转给 AI 员工，其他渠道只转给真人客服；其他来源只能交还接待发起人的 AI 员工。
  const channelSource = customer?.source === ServiceSource.ServiceSourceChannel
  const people = assignees.filter(
    (assignee) =>
      assignee.identityId !== currentIdentityId &&
      (assignee.type !== OrganizationIdentityType.OrganizationIdentityTypeAgent ||
        (channelSource && customerReplySupported(customer))),
  )
  const transferCandidates: InboxAssignee[] =
    !channelSource && customer?.agentIdentityId
      ? [
          ...people,
          {
            identityId: customer.agentIdentityId,
            type: OrganizationIdentityType.OrganizationIdentityTypeAgent,
            displayName: customer.agentName ?? "",
            avatarUrl: "",
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
      if (recoverSession(error, navigate)) return
      console.warn("更新客服会话失败", {
        conversationId: conversation.id,
        operation: nextOperation,
        error,
      })
      toast.error(
        isApiError(error) ? apiErrorMessage(error) : t("conversationActionError"),
      )
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
