/** AI 员工基本资料表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  FilePurpose,
  UserStatus,
  updateAgent,
  type AgentData,
  type Team,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { ImagePicker } from "@/components/image-picker"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import {
  selectableWorkStatuses,
  workStatusLabel,
} from "@/components/work-status"
import { TeamSelectField } from "@/components/form/team-select-field"
import { AgentResponsibleField } from "@/features/agents/agent-responsible-field"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import {
  createAgentProfileSchema,
  type AgentProfileFormValues,
} from "@/features/agents/agent-schema"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { requestErrorMessage } from "@/lib/form-errors"
import { useAutoSave } from "@/hooks/use-auto-save"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 单独保存 AI 员工头像、名称、工作状态、所属团队、服务对象、转人工团队和负责人。 */
export function AgentProfileForm({
  agent,
  teams,
  onSaved,
}: {
  agent: AgentData
  teams: Team[]
  onSaved: () => void
}) {
  const { t } = useTranslation(["agents", "contacts", "common"])
  const { t: tCommon } = useTranslation("common")
  const navigate = useNavigate()
  const schema = useMemo(
    () =>
      createAgentProfileSchema({
        nameRequired: t("validation.nameRequired"),
        nameInvalid: t("validation.nameInvalid"),
      }),
    [t],
  )
  const form = useForm<AgentProfileFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      displayName: agent.displayName,
      workStatus: agent.workStatus,
      teamIds: agent.teams.map((team) => team.id),
      serviceAudiences: agent.serviceAudiences,
      handoffTeamId: agent.handoffTeamId ?? "",
      responsibleUserId: agent.responsible?.userId ?? "",
    },
  })
  const avatar = usePendingImageUpload({
    purpose: FilePurpose.FilePurposeAgentAvatar,
    onError: (error) => {
      console.warn("上传 AI 员工头像失败", { agent_id: agent.id, error })
      if (!recoverSession(error, navigate)) toast.error(t("contacts:avatar.uploadError"))
    },
  })
  const { mounted, dirty, discarded } = useFormLifetime(
    form.formState.isDirty || avatar.pending !== null,
  )

  // 资料刷新时同步未修改的表单，保留正在编辑的草稿。
  useEffect(() => {
    if (dirty.current) return
    form.reset({
      displayName: agent.displayName,
      workStatus: agent.workStatus,
      teamIds: agent.teams.map((team) => team.id),
      serviceAudiences: agent.serviceAudiences,
      handoffTeamId: agent.handoffTeamId ?? "",
      responsibleUserId: agent.responsible?.userId ?? "",
    })
  }, [agent, dirty, form])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit, discarded })

  /** 提交基本资料和待保存的头像，并保留其他页签的编辑内容。 */
  async function submit(values: AgentProfileFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return false
    try {
      await updateAgent(agent.id, { ...values, avatarFileId })
      onSaved()
      if (!mounted.current) return true
      dirty.current = !acceptSaved(values)
      avatar.clear(avatarFileId)
      return true
    } catch (error) {
      // 离开页面后提交的改动失败时同样提示。
      if (recoverSession(error, navigate)) return false
      console.warn("保存 AI 员工基本资料失败", { agent_id: agent.id, error })
      toast.error(
        requestErrorMessage(error, [
          "displayName",
          "workStatus",
          "teamIds",
          "serviceAudiences",
          "handoffTeamId",
          "responsibleUserId",
        ]),
      )
      return false
    }
  }

  return (
    <form onSubmit={form.handleSubmit(() => saveNow(true))} noValidate>
      <FieldGroup>
        <Field>
          <FieldLabel>{t("contacts:avatar.label")}</FieldLabel>
          <ImagePicker
            imageURL={avatar.pending?.previewURL || agent.avatarUrl}
            fallback="agent"
            label={t("contacts:avatar.choose")}
            disabled={form.formState.isSubmitting}
            loading={avatar.pending?.status === "uploading"}
            onSelect={avatar.select}
          />
        </Field>
        <FormInputField
          name="displayName"
          id="agent-profile-name"
          control={form.control}
          label={t("form.name")}
          disabled={form.formState.isSubmitting}
        />
        <Controller
          name="teamIds"
          control={form.control}
          render={({ field }) => (
            <TeamSelectField
              teams={teams}
              label={t("form.teams")}
              emptyMessage={t("form.noTeams")}
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              disabled={form.formState.isSubmitting}
            />
          )}
        />
        <Controller
          name="workStatus"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="agent-profile-work-status" required>
                {t("contacts:columns.workStatus")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id="agent-profile-work-status"
                required
                aria-invalid={fieldState.invalid}
                disabled={
                  form.formState.isSubmitting ||
                  agent.status !== UserStatus.UserStatusActive
                }
              >
                {selectableWorkStatuses.map((status) => (
                  <option key={status} value={status}>
                    {workStatusLabel(status, tCommon)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        <Controller
          name="serviceAudiences"
          control={form.control}
          render={({ field }) => (
            <AgentServiceAudiencesField
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              disabled={form.formState.isSubmitting}
            />
          )}
        />
        <Controller
          name="handoffTeamId"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="agent-profile-handoff-team">
                {t("form.handoffTeam")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id="agent-profile-handoff-team"
                aria-invalid={fieldState.invalid}
                disabled={form.formState.isSubmitting}
              >
                <option value="">{t("form.publicQueue")}</option>
                {teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>{t("form.handoffTeamHelp")}</FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="responsibleUserId"
          control={form.control}
          render={({ field, fieldState }) => (
            <AgentResponsibleField
              {...field}
              responsible={agent.responsible}
              invalid={fieldState.invalid}
              disabled={form.formState.isSubmitting}
            />
          )}
        />
      </FieldGroup>
    </form>
  )
}
