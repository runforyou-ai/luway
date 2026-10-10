/** AI 员工基本资料表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  FilePurpose,
  OperationLevel,
  UserStatus,
  computerOperationLevels,
  listWorkspaceComputers,
  updateAgent,
  type AgentData,
  type Team,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { TeamSelectField } from "@/components/form/team-select-field"
import { ImagePicker } from "@/components/image-picker"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { selectableWorkStatuses, workStatusLabel } from "@/components/work-status"
import {
  AgentComputerField,
  AgentComputerGrantField,
  type ComputerGrantValue,
} from "@/features/agents/agent-computer-field"
import { AgentResponsibleField } from "@/features/agents/agent-responsible-field"
import { createAgentProfileSchema, type AgentProfileFormValues } from "@/features/agents/agent-schema"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import { LocalAgentsField } from "@/features/agents/local-agents-field"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 返回 AI 员工当前的工作区电脑授权，未使用电脑或授权不在可选级别中时默认只读取文件。 */
function computerGrant(agent: AgentData): ComputerGrantValue {
  const level = computerOperationLevels.find((item) => item === agent.computer?.grant.maxLevel)
  if (!agent.computer || !level) return { maxLevel: OperationLevel.L0, confirmL2: false }
  return { maxLevel: level, confirmL2: agent.computer.grant.confirmL2 }
}

/** 单独保存 AI 员工头像、名称、工作状态、所属团队、服务对象、转人工团队、负责人和工作区电脑及其授权与启用的本机 Agent。 */
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
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () =>
      createAgentProfileSchema({
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
      computerId: agent.computer?.id ?? "",
      computerGrant: computerGrant(agent),
      localAgents: agent.computer?.localAgents ?? [],
    },
  })
  const computerId = useWatch({ control: form.control, name: "computerId" })
  const computers = useResource(resourceKeys.workspaceComputers(), listWorkspaceComputers, { staleTime: 0 })
  const computerLocalAgents = computers.data?.computers.find((item) => item.id === computerId)?.localAgents ?? []
  const avatar = usePendingImageUpload({
    purpose: FilePurpose.AgentAvatar,
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
      computerId: agent.computer?.id ?? "",
      computerGrant: computerGrant(agent),
      localAgents: agent.computer?.localAgents ?? [],
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
      reportError(error, {
        log: "保存 AI 员工基本资料",
        context: { agent_id: agent.id },
        fields: [
          "displayName",
          "workStatus",
          "teamIds",
          "serviceAudiences",
          "handoffTeamId",
          "responsibleUserId",
          "computerId",
          "computerGrant",
          "localAgents",
        ],
      })
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
                  agent.status !== UserStatus.Active
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
        <Controller
          name="computerId"
          control={form.control}
          render={({ field, fieldState }) => (
            <AgentComputerField
              {...field}
              computer={agent.computer}
              invalid={fieldState.invalid}
              disabled={form.formState.isSubmitting}
            />
          )}
        />
        {computerId ? (
          <Controller
            name="computerGrant"
            control={form.control}
            render={({ field }) => (
              <AgentComputerGrantField value={field.value} onChange={field.onChange} disabled={form.formState.isSubmitting} />
            )}
          />
        ) : null}
        {computerId ? (
          <Controller
            name="localAgents"
            control={form.control}
            render={({ field }) => (
              <LocalAgentsField
                id="agent-profile-local-agents"
                available={computerLocalAgents}
                value={field.value}
                onChange={field.onChange}
                disabled={form.formState.isSubmitting}
              />
            )}
          />
        ) : null}
      </FieldGroup>
    </form>
  )
}
