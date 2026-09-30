/** 将会话系统事件转换为当前语言的时间线文案。 */
import type { TFunction } from "i18next"

import {
  ConversationSystemEventType,
  ServiceRequestStatus,
  ServiceSessionCloseReason,
  ServiceSessionReturnReason,
  ServiceSessionTargetKind,
  type ConversationMessageData,
  type ConversationSystemEventParticipant,
} from "@/api"
import { handoffReasonKey } from "@/lib/handoff-reason-labels"

type TimelineSystemEvent = NonNullable<ConversationMessageData["systemEvent"]>

type TimelineTranslate = TFunction<["inbox", "common"]>

/** 按当前语言连接系统事件中的成员姓名。 */
function formatGroupParticipantNames(names: string[], t: TimelineTranslate) {
  if (names.length < 2) return names[0] ?? ""
  if (names.length === 2) {
    return names.join(t("groupSystemListPairSeparator"))
  }
  const previousNames = names.slice(0, -1).join(t("groupSystemListSeparator"))
  return `${previousNames}${t("groupSystemListFinalSeparator")}${names[names.length - 1]}`
}

/** 客服处理周期去向的时间线文案：成员取名称快照，团队与公共队列按队列文案展示。 */
function sessionTargetText(
  target: TimelineSystemEvent["sessionTarget"],
  currentIdentityID: string,
  t: TimelineTranslate,
) {
  if (target?.kind === ServiceSessionTargetKind.ServiceSessionTargetMember) {
    return target.identityId === currentIdentityID
      ? t("messageSenderYou")
      : (target.displayName ?? t("unknownSender"))
  }
  return target?.kind === ServiceSessionTargetKind.ServiceSessionTargetTeam
    ? t("handoffTargetTeam", { name: target.teamName ?? "" })
    : t("handoffTargetPublicQueue")
}

/** 将类型化系统事件转换为当前语言的时间线文案。 */
export function formatSystemEvent(
  event: TimelineSystemEvent,
  currentIdentityID: string,
  t: TimelineTranslate,
) {
  // 转人工事件按去向与原因码本地化，名称与咨询分类取事件写入时的快照。
  if (event.type === ConversationSystemEventType.ConversationSystemEventServiceSessionHandedOff) {
    return t(event.categoryName ? "serviceSessionHandedOffWithCategory" : "serviceSessionHandedOff", {
      agent: event.fromDisplayName ?? t("unknownSender"),
      target: sessionTargetText(event.sessionTarget, currentIdentityID, t),
      reason: t(handoffReasonKey(event.handoffReason)),
      category: event.categoryName ?? "",
    })
  }
  // 退回队列事件没有操作人，按退回原因展示原负责人与退回去向。
  if (
    event.type ===
    ConversationSystemEventType.ConversationSystemEventServiceSessionReturned
  ) {
    const key =
      event.returnReason === ServiceSessionReturnReason.ServiceSessionReturnResponseTimeout
        ? "serviceSessionReturnedResponseTimeout"
        : "serviceSessionReturned"
    return t(key, {
      from:
        event.fromIdentityId === currentIdentityID
          ? t("messageSenderYou")
          : (event.fromDisplayName ?? t("unknownSender")),
      target: sessionTargetText(event.sessionTarget, currentIdentityID, t),
    })
  }
  // 自动分配事件没有操作人，只展示承接成员。
  if (
    event.type ===
    ConversationSystemEventType.ConversationSystemEventServiceSessionAssigned
  ) {
    return t("serviceSessionAssigned", {
      target: sessionTargetText(event.sessionTarget, currentIdentityID, t),
    })
  }
  // 访客评价事件没有操作人，按是否解决展示结果，评语由时间线另起一行展示。
  if (
    event.type ===
    ConversationSystemEventType.ConversationSystemEventServiceSessionRated
  ) {
    return t(
      event.ratingResolved
        ? "serviceSessionRatedResolved"
        : "serviceSessionRatedUnresolved",
    )
  }
  // 邮件事件没有操作人，展示访客接收回复的邮箱。
  if (
    event.type ===
    ConversationSystemEventType.ConversationSystemEventServiceSessionEmailCollected
  ) {
    return t("serviceSessionEmailCollected", { email: event.email ?? "" })
  }
  if (
    event.type ===
    ConversationSystemEventType.ConversationSystemEventServiceSessionEmailNotified
  ) {
    return t("serviceSessionEmailNotified", { email: event.email ?? "" })
  }
  // 服务进度只对企业成员发起人展示，按进度与去向快照说明。
  if (event.type === ConversationSystemEventType.ConversationSystemEventServiceStatusChanged) {
    if (event.serviceStatus === ServiceRequestStatus.ServiceRequestStatusProcessing) {
      return t("serviceStatusProcessing", { name: event.sessionTarget?.displayName ?? t("unknownSender") })
    }
    if (event.serviceStatus === ServiceRequestStatus.ServiceRequestStatusClosed) {
      return t(event.closeReason === ServiceSessionCloseReason.ServiceSessionCloseCustomerUnresponsive
        ? "serviceStatusClosedUnresponsive"
        : "serviceStatusResolved")
    }
    return event.sessionTarget?.kind === ServiceSessionTargetKind.ServiceSessionTargetTeam
      ? t("serviceStatusHandedOffTeam", { team: event.sessionTarget.teamName ?? "" })
      : t("serviceStatusHandedOff")
  }
  // 成员取事件写入时的名称快照，助理按「主人的助理 · 名称」展示。
  const participantName = (
    participant: ConversationSystemEventParticipant,
  ) =>
    participant.identityId === currentIdentityID
      ? t("messageSenderYou")
      : participant.assistantOwnerName
        ? t("assistantDisplayName", { owner: participant.assistantOwnerName, name: participant.displayName })
        : participant.displayName
  const actor = participantName(event.actor)
  const targets = formatGroupParticipantNames(
    event.targets.map(participantName),
    t,
  )
  switch (event.type) {
    case ConversationSystemEventType.ConversationSystemEventServiceSessionClaimed:
      return t("serviceSessionClaimed", { actor })
    case ConversationSystemEventType.ConversationSystemEventServiceSessionTakenOver:
      return t("serviceSessionTakenOver", {
        actor,
        from:
          event.fromIdentityId === currentIdentityID
            ? t("messageSenderYou")
            : (event.fromDisplayName ?? t("unknownSender")),
      })
    case ConversationSystemEventType.ConversationSystemEventServiceSessionTransferred:
      return t("serviceSessionTransferred", {
        actor,
        target: sessionTargetText(event.sessionTarget, currentIdentityID, t),
      })
    case ConversationSystemEventType.ConversationSystemEventServiceSessionClosed:
      // AI 负责人关闭的周期按结束方式说明关闭原因。
      if (event.closeReason === ServiceSessionCloseReason.ServiceSessionCloseAIResolved) {
        return t("serviceSessionClosedAIResolved", { actor })
      }
      if (event.closeReason === ServiceSessionCloseReason.ServiceSessionCloseCustomerUnresponsive) {
        return t("serviceSessionClosedCustomerUnresponsive", { actor })
      }
      return t("serviceSessionClosed", { actor })
    case ConversationSystemEventType.ConversationSystemEventServiceSessionReopened:
      return t("serviceSessionReopened", { actor })
    case ConversationSystemEventType.ConversationSystemEventGroupRenamed:
      // 未命名的群首次命名或清除名称时分别说明。
      if (!event.title) return t("groupSystemTitleCleared", { actor })
      if (!event.previousTitle) return t("groupSystemTitleSet", { actor, title: event.title })
      return t("groupSystemRenamed", {
        actor,
        previousTitle: event.previousTitle,
        title: event.title,
      })
    case ConversationSystemEventType.ConversationSystemEventGroupMembersAdded:
      return t("groupSystemMembersAdded", { actor, targets })
    case ConversationSystemEventType.ConversationSystemEventGroupMemberRemoved:
      return t("groupSystemMemberRemoved", {
        actor,
        target: targets,
      })
    case ConversationSystemEventType.ConversationSystemEventGroupMemberLeft:
      return t("groupSystemMemberLeft", { actor })
    case ConversationSystemEventType.ConversationSystemEventGroupOwnerTransferred:
      return t("groupSystemOwnerTransferred", {
        actor,
        target: targets,
      })
    case ConversationSystemEventType.ConversationSystemEventGroupDissolved:
      return t("groupSystemDissolved", { actor })
    default:
      return t("groupSystemUpdated")
  }
}
