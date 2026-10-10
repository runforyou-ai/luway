/** 个人 AI 员工新建与编辑表单。 */
import { useEffect, useMemo, type ReactNode } from "react"
import { useMutation } from "@tanstack/react-query"
import { Controller, useForm, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  AgentExecutionMode,
  createPersonalAgent,
  listComputers,
  updatePersonalAgent,
  type PersonalAgentDetailData,
  type ServiceAudience,
  type ComputerLocalAgent,
} from "@/api"
import { AgentBusinessSystemsField } from "@/components/agent-fields/agent-business-systems-field"
import { AgentKnowledgeField } from "@/components/agent-fields/agent-knowledge-field"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ImagePicker } from "@/components/image-picker"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useAgentAvatarUpload, type AgentAvatarUpload, type AgentCreateDraft } from "@/features/agents/agent-form"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import { LocalAgentsField } from "@/features/agents/local-agents-field"
import {
  createPersonalAgentSchema,
  type PersonalAgentFormValues,
} from "@/features/agents/personal/personal-agent-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePersonalAgentInvalidator } from "@/hooks/use-personal-agent-invalidator"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 服务端字段错误可对应的表单字段。 */
const personalAgentErrorFields = ["displayName", "modelId", "systemInstruction", "knowledgeBaseIds", "businessSystems", "localAgents"]

/** 创建个人 AI 员工表单的校验规则。 */
function usePersonalAgentSchema() {
  const { t } = useTranslation(["agents", "contacts"])
  return useMemo(
    () =>
      createPersonalAgentSchema({
        nameInvalid: t("validation.nameInvalid"),
        instructionTooLong: t("personal.validation.instructionTooLong"),
      }),
    [t],
  )
}

/** 把表单值转换为个人 AI 员工的资料、执行配置、业务系统授权与启用的本机 Agent 输入。 */
function personalAgentInput(values: PersonalAgentFormValues, avatarFileId: string) {
  return {
    displayName: values.displayName,
    avatarFileId,
    execution: {
      mode: AgentExecutionMode.Managed,
      managed: {
        modelId: values.modelId,
        systemInstruction: values.systemInstruction,
        knowledgeBaseIds: values.knowledgeBaseIds,
      },
    },
    businessSystems: values.businessSystems,
    localAgents: values.localAgents,
  }
}

/** 在本机创建仅服务自己的 AI 员工，使用的电脑固定为当前电脑；服务对象改为客户或员工时把所选对象与已填内容交给 onServiceAudiencesChange 切换表单，serviceAvailable 为 false 时不能改为客户或员工；头像上传状态由创建页共享。 */
export function PersonalAgentCreateForm({
  computerID,
  computerName,
  draft,
  draftDirty,
  avatar,
  serviceAvailable = true,
  onServiceAudiencesChange,
  onSaved,
  onCancel,
}: {
  computerID: string
  computerName: string
  draft: AgentCreateDraft
  draftDirty: boolean
  avatar: AgentAvatarUpload
  serviceAvailable?: boolean
  onServiceAudiencesChange: (audiences: ServiceAudience[], draft: AgentCreateDraft) => void
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation(["agents", "contacts"])
  const reportError = useRequestErrorReporter()
  const invalidate = usePersonalAgentInvalidator()
  const schema = usePersonalAgentSchema()
  const form = useForm<PersonalAgentFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { ...draft, businessSystems: [], localAgents: [] },
  })
  const { dirty } = useFormLifetime(form.formState.isDirty || avatar.pending !== null || draftDirty)
  const computers = useResource(resourceKeys.computers(), listComputers)
  const localAgents = computers.data?.computers.find((computer) => computer.id === computerID)?.localAgents ?? []
  // 头像上传失败时不创建，结果为 false。
  const creation = useMutation({
    mutationFn: async (values: PersonalAgentFormValues) => {
      const avatarFileId = await avatar.ensureUploaded()
      if (avatarFileId === null) return false
      await createPersonalAgent({ ...personalAgentInput(values, avatarFileId), computerId: computerID })
      return true
    },
    onSuccess: (created) => {
      if (created) void invalidate()
    },
  })
  const saving = form.formState.isSubmitting

  /** 切换为服务客户或员工时带上已填写的名称与托管执行配置。 */
  function switchToService(audiences: ServiceAudience[]) {
    const values = form.getValues()
    onServiceAudiencesChange(audiences, {
      displayName: values.displayName,
      modelId: values.modelId,
      systemInstruction: values.systemInstruction,
      knowledgeBaseIds: values.knowledgeBaseIds,
    })
  }

  /** 上传待保存的头像后创建个人 AI 员工。 */
  function submit(values: PersonalAgentFormValues) {
    // 失败由 onError 提示；返回请求让提交状态覆盖整个请求。
    return creation.mutateAsync(values, {
      onSuccess: (created) => {
        if (!created) return
        toast.success(t("form.created"))
        dirty.current = false
        form.reset(values)
        avatar.clear()
        onSaved()
      },
      onError: (error) => reportError(error, { log: "创建个人 AI 员工", fields: personalAgentErrorFields }),
    }).catch(() => undefined)
  }

  return (
    <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <PersonalAgentFields
        control={form.control}
        disabled={saving}
        avatarURL={avatar.pending?.previewURL}
        avatarLoading={avatar.pending?.status === "uploading"}
        onAvatarSelect={avatar.select}
        computerName={computerName}
        localAgents={localAgents}
        leading={
          <AgentServiceAudiencesField
            value={[]}
            onChange={(audiences) => switchToService(audiences)}
            onBlur={() => undefined}
            disabled={saving}
            serviceDisabled={!serviceAvailable}
            personal={{ checked: true, available: true, locked: !serviceAvailable, onChange: (checked) => !checked && switchToService([]) }}
          />
        }
      />
      <FormActions saving={saving} onCancel={onCancel} />
    </form>
  )
}

/** 编辑当前成员负责的个人 AI 员工，改动自动保存为新的配置版本。 */
export function PersonalAgentEditForm({
  detail,
  onSaved,
}: {
  detail: PersonalAgentDetailData
  onSaved: () => void
}) {
  const reportError = useRequestErrorReporter()
  const schema = usePersonalAgentSchema()
  const { personalAgent: agent, execution } = detail
  const values = useMemo<PersonalAgentFormValues>(
    () => ({
      displayName: agent.displayName,
      modelId: execution.managed.model.id,
      systemInstruction: execution.managed.systemInstruction,
      knowledgeBaseIds: execution.managed.knowledgeBaseIds,
      businessSystems: execution.businessSystems,
      localAgents: agent.localAgents,
    }),
    [agent.displayName, agent.localAgents, execution],
  )
  const form = useForm<PersonalAgentFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: values,
  })
  const avatar = useAgentAvatarUpload()
  const { mounted, dirty, discarded } = useFormLifetime(form.formState.isDirty || avatar.pending !== null)

  // 详情刷新时同步未修改的表单，保留正在编辑的草稿。
  useEffect(() => {
    if (!dirty.current) form.reset(values)
  }, [values, dirty, form])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit, discarded })

  /** 提交资料、执行配置与待保存的头像。 */
  async function submit(next: PersonalAgentFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return false
    try {
      await updatePersonalAgent(agent.id, personalAgentInput(next, avatarFileId))
      onSaved()
      if (!mounted.current) return true
      dirty.current = !acceptSaved(next)
      avatar.clear(avatarFileId)
      return true
    } catch (error) {
      reportError(error, {
        log: "保存个人 AI 员工",
        context: { agent_id: agent.id },
        fields: personalAgentErrorFields,
      })
      return false
    }
  }

  return (
    <form onSubmit={form.handleSubmit(() => saveNow(true))} noValidate>
      <PersonalAgentFields
        control={form.control}
        disabled={form.formState.isSubmitting}
        avatarURL={avatar.pending?.previewURL || agent.avatarUrl}
        avatarLoading={avatar.pending?.status === "uploading"}
        onAvatarSelect={(file) => {
          avatar.select(file)
          // 头像不在表单值中，选择后立即排队保存。
          saveNow(true)
        }}
        computerName={agent.computer.name}
        localAgents={agent.computer.localAgents}
      />
    </form>
  )
}

/** 渲染个人 AI 员工的头像、名称、对话模型、指令、知识库、业务系统授权、只读的使用电脑与电脑上可启用的本机 Agent。 */
function PersonalAgentFields({
  control,
  disabled,
  avatarURL,
  avatarLoading,
  onAvatarSelect,
  computerName,
  localAgents,
  leading,
}: {
  control: Control<PersonalAgentFormValues>
  disabled: boolean
  avatarURL?: string
  avatarLoading: boolean
  onAvatarSelect: (file: File) => void
  computerName: string
  localAgents: ComputerLocalAgent[]
  leading?: ReactNode
}) {
  const { t } = useTranslation(["agents", "contacts"])
  return (
    <FieldGroup>
      {leading}
      <Field>
        <FieldLabel>{t("contacts:avatar.label")}</FieldLabel>
        <ImagePicker
          imageURL={avatarURL}
          fallback="agent"
          label={t("contacts:avatar.choose")}
          disabled={disabled}
          loading={avatarLoading}
          onSelect={onAvatarSelect}
        />
      </Field>
      <FormInputField
        name="displayName"
        id="agent-name"
        control={control}
        label={t("form.name")}
        disabled={disabled}
      />
      <AgentModelField control={control} name="modelId" disabled={disabled} />
      <Controller
        name="systemInstruction"
        control={control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor="agent-instruction">{t("personal.form.instruction")}</FieldLabel>
            <Textarea
              {...field}
              id="agent-instruction"
              rows={6}
              disabled={disabled}
              aria-invalid={fieldState.invalid}
            />
          </Field>
        )}
      />
      <Controller
        name="knowledgeBaseIds"
        control={control}
        render={({ field }) => (
          <Field>
            <FieldLabel>{t("execution.knowledgeBases")}</FieldLabel>
            <AgentKnowledgeField value={field.value} onChange={field.onChange} disabled={disabled} />
          </Field>
        )}
      />
      <Controller
        name="businessSystems"
        control={control}
        render={({ field }) => (
          <Field>
            <FieldLabel>{t("businessSystems.label")}</FieldLabel>
            <FieldDescription>{t("personal.form.businessSystemsHelp")}</FieldDescription>
            <AgentBusinessSystemsField value={field.value} onChange={field.onChange} disabled={disabled} />
          </Field>
        )}
      />
      <Field>
        <FieldLabel htmlFor="agent-computer">{t("personal.form.computer")}</FieldLabel>
        <Input id="agent-computer" value={computerName} readOnly disabled />
        <FieldDescription>{t("personal.form.computerHelp")}</FieldDescription>
      </Field>
      <Controller
        name="localAgents"
        control={control}
        render={({ field }) => (
          <LocalAgentsField id="agent-local-agents" available={localAgents} value={field.value} onChange={field.onChange} disabled={disabled} />
        )}
      />
    </FieldGroup>
  )
}
