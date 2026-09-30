/** 各消息渠道共用的接待设置校验。 */
import { z } from "zod"

import { ChannelRoutingTargetType, type ChannelRoutingTarget } from "@/api"
import { requiredWailsEnum } from "@/lib/wails-enum"

interface ChannelReceptionValidationMessages {
  teamRequired: string
  memberRequired: string
  fallbackDifferent: string
}

interface ChannelReceptionValues {
  newConversationTarget: ChannelRoutingTarget
  fallbackTarget: ChannelRoutingTarget
}

/** 创建接待目标字段校验。 */
export function createChannelReceptionFields(
  messages: ChannelReceptionValidationMessages,
) {
  const target = z
    .object({
      type: requiredWailsEnum(ChannelRoutingTargetType),
      id: z.string(),
    })
    .superRefine((value, context) => {
      if (
        value.type !==
          ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue &&
        !value.id
      ) {
        context.addIssue({
          code: "custom",
          path: ["id"],
          message:
            value.type === ChannelRoutingTargetType.ChannelRoutingTargetTypeTeam
              ? messages.teamRequired
              : messages.memberRequired,
        })
      }
    })
  return {
    newConversationTarget: target,
    // 失败去向只能是团队或公共队列。
    fallbackTarget: target.refine(
      (value) =>
        value.type !==
        ChannelRoutingTargetType.ChannelRoutingTargetTypeMember,
      { path: ["type"], message: messages.teamRequired },
    ),
  }
}

/** 校验接待失败团队与新会话团队的互异性。 */
export function validateChannelReceptionFallback(
  value: ChannelReceptionValues,
  context: z.RefinementCtx,
  message: string,
) {
  if (
    value.newConversationTarget.type !==
      ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue &&
    value.newConversationTarget.type === value.fallbackTarget.type &&
    value.newConversationTarget.id === value.fallbackTarget.id
  ) {
    context.addIssue({
      code: "custom",
      path: ["fallbackTarget", "id"],
      message,
    })
  }
}

/** 创建独立接待设置表单校验。 */
export function createChannelReceptionSchema(
  messages: ChannelReceptionValidationMessages,
) {
  return z
    .object(createChannelReceptionFields(messages))
    .superRefine((value, context) =>
      validateChannelReceptionFallback(
        value,
        context,
        messages.fallbackDifferent,
      ),
    )
}

export type ChannelReceptionSettingsFormValues = z.infer<
  ReturnType<typeof createChannelReceptionSchema>
>
