/** 各消息渠道共用的接待设置字段和候选项加载。 */
import { useEffect } from "react"
import {
  Controller,
  type Control,
  type FieldValues,
  type Path,
} from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  ChannelRoutingTargetType,
  ChannelType,
  OrganizationIdentityType,
  listServiceAssignees,
  listAllTeams,
  type ChannelRoutingTarget,
  type InboxAssignee,
  type Team,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { Field, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import type { ChannelReceptionSettingsFormValues } from "@/features/channels/reception/channel-reception-schema"

type ReceptionTargetName = keyof ChannelReceptionSettingsFormValues

const newConversationChoices = [
  ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue,
  ChannelRoutingTargetType.ChannelRoutingTargetTypeTeam,
  ChannelRoutingTargetType.ChannelRoutingTargetTypeMember,
] as const

// 失败去向只能交给团队或公共队列。
const fallbackChoices = [
  ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue,
  ChannelRoutingTargetType.ChannelRoutingTargetTypeTeam,
] as const

/** 显示接待目标字段。 */
function ReceptionTargetField<
  TValues extends FieldValues & ChannelReceptionSettingsFormValues,
>({
  name,
  control,
  teams,
  assignees,
}: {
  name: ReceptionTargetName
  control: Control<TValues>
  teams: Team[]
  assignees: InboxAssignee[]
}) {
  const { t } = useTranslation("channels")
  const isFallback = name === "fallbackTarget"

  return (
    <Controller
      name={`${name}.type` as Path<TValues>}
      control={control}
      render={({ field: typeField, fieldState: typeFieldState }) => (
        <Controller
          name={`${name}.id` as Path<TValues>}
          control={control}
          render={({ field: idField, fieldState: idFieldState }) => {
            const targetType = typeField.value as ChannelRoutingTarget["type"]
            const invalid = typeFieldState.invalid || idFieldState.invalid
            return (
              <Field data-invalid={invalid}>
                <FieldLabel htmlFor={`${name}-type`} required>
                  {t(
                    isFallback ? "routing.fallback" : "routing.newConversation",
                  )}
                </FieldLabel>
                <NativeSelect
                  {...typeField}
                  id={`${name}-type`}
                  value={targetType}
                  aria-invalid={typeFieldState.invalid}
                  onChange={(event) => {
                    typeField.onChange(event)
                    idField.onChange("")
                  }}
                >
                  {isFallback
                    ? fallbackChoices.map((choice) => (
                        <option key={choice} value={choice}>
                          {t(`routing.fallbackTypes.${choice}`)}
                        </option>
                      ))
                    : newConversationChoices.map((choice) => (
                        <option key={choice} value={choice}>
                          {t(`routing.newConversationTypes.${choice}`)}
                        </option>
                      ))}
                </NativeSelect>
                {targetType !==
                ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue ? (
                  <div className="mt-3 flex w-full flex-col gap-2">
                    <FieldLabel htmlFor={`${name}-id`} required>
                      {isFallback
                        ? t("routing.targetLabels.fallback.team")
                        : targetType ===
                            ChannelRoutingTargetType.ChannelRoutingTargetTypeTeam
                          ? t("routing.targetLabels.newConversation.team")
                          : t("routing.targetLabels.newConversation.member")}
                    </FieldLabel>
                    <NativeSelect
                      {...idField}
                      id={`${name}-id`}
                      value={idField.value as string}
                      required
                      aria-invalid={idFieldState.invalid}
                    >
                      <option value="">{t("routing.select")}</option>
                      {targetType ===
                      ChannelRoutingTargetType.ChannelRoutingTargetTypeTeam
                        ? teams.map((team) => (
                            <option key={team.id} value={team.id}>
                              {team.name}
                            </option>
                          ))
                        : assignees.map((assignee) => (
                            <option
                              key={assignee.identityId}
                              value={assignee.identityId}
                            >
                              {assignee.displayName}（
                              {t(
                                assignee.type ===
                                  OrganizationIdentityType.OrganizationIdentityTypeAgent
                                  ? "routing.agent"
                                  : "routing.person",
                              )}
                              ）
                            </option>
                          ))}
                    </NativeSelect>
                  </div>
                ) : null}
              </Field>
            )
          }}
        />
      )}
    />
  )
}

/** 渲染可被不同渠道表单复用的接待设置字段。 */
export function ChannelReceptionSettingsFields<
  TValues extends FieldValues & ChannelReceptionSettingsFormValues,
>({
  control,
  channelType,
}: {
  control: Control<TValues>
  channelType: ChannelType
}) {
  const { t } = useTranslation("channels")
  const { data: options, error } = useResource(
    resourceKeys.channelReceptionOptions(),
    async () => {
      const [teams, assignees] = await Promise.all([
        listAllTeams(),
        listServiceAssignees(),
      ])
      return { teams, assignees }
    },
    { staleTime: 0 },
  )
  const teams = options?.teams ?? []
  const assignees = (options?.assignees ?? []).filter(
    (assignee) =>
      channelType === ChannelType.ChannelTypeWebsite ||
      channelType === ChannelType.ChannelTypeTelegram ||
      assignee.type !==
        OrganizationIdentityType.OrganizationIdentityTypeAgent,
  )

  /** 候选项加载失败时提示用户。 */
  useEffect(() => {
    if (error) {
      console.warn("渠道接待候选项加载失败", error)
      toast.error(t("routing.loadError"))
    }
  }, [error, t])

  return (
    <>
      {(["newConversationTarget", "fallbackTarget"] as const).map((name) => (
        <ReceptionTargetField
          key={name}
          name={name}
          control={control}
          teams={teams}
          assignees={assignees}
        />
      ))}
    </>
  )
}
