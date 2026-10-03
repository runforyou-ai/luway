/** 个人 AI 员工新建与编辑表单。 */
import { useEffect, useMemo, type ReactNode } from "react"
import { Controller, useForm, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AgentExecutionMode,
  createPersonalAgent,
  updatePersonalAgent,
  type PersonalAgentDetailData,
  type ServiceAudience,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ImagePicker } from "@/components/image-picker"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { AgentKnowledgeField } from "@/components/agent-fields/agent-knowledge-field"
import { AgentMCPField } from "@/components/agent-fields/agent-mcp-field"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import { usePersonalAgentInvalidator } from "@/hooks/use-personal-agent-invalidator"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import { useAgentAvatarUpload, type AgentAvatarUpload, type AgentCreateDraft } from "@/features/agents/agent-form"
import {
  createPersonalAgentSchema,
  type PersonalAgentFormValues,
} from "@/features/agents/personal/personal-agent-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

const personalAgentErrorFields = ["displayName", "modelId", "systemInstruction", "knowledgeBaseIds", "mcpServerIds"]

/** 创建个人 AI 员工表单的校验规则。 */
function usePersonalAgentSchema() {
  const { t } = useTranslation(["agents", "contacts"])
  return useMemo(
    () =>
      createPersonalAgentSchema({
        nameRequired: t("validation.nameRequired"),
        nameInvalid: t("validation.nameInvalid"),
        modelRequired: t("validation.modelRequired"),
        instructionTooLong: t("personal.validation.instructionTooLong"),
      }),
    [t],
  )
}

/** 把表单值转换为个人 AI 员工的资料、执行配置与企业 MCP 服务输入。 */
function personalAgentInput(values: PersonalAgentFormValues, avatarFileId: string) {
  return {
    displayName: values.displayName,
    avatarFileId,
    execution: {
      mode: AgentExecutionMode.AgentExecutionModeManaged,
      managed: {
        modelId: values.modelId,
        systemInstruction: values.systemInstruction,
        knowledgeBaseIds: values.knowledgeBaseIds,
      },
    },
    mcpServerIds: values.mcpServerIds,
  }
}

/** 在本机创建仅服务自己的 AI 员工，使用的电脑固定为当前电脑；服务对象改为客户或员工时把所选对象与已填内容交给 onServiceAudiencesChange 切换表单，头像上传状态由创建页共享。 */
export function PersonalAgentCreateForm({
  computerID,
  computerName,
  draft,
  draftDirty,
  avatar,
  onServiceAudiencesChange,
  onSaved,
  onCancel,
}: {
  computerID: string
  computerName: string
  draft: AgentCreateDraft
  draftDirty: boolean
  avatar: AgentAvatarUpload
  onServiceAudiencesChange: (audiences: ServiceAudience[], draft: AgentCreateDraft) => void
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation(["agents", "contacts"])
  const navigate = useNavigate()
  const invalidate = usePersonalAgentInvalidator()
  const schema = usePersonalAgentSchema()
  const form = useForm<PersonalAgentFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { ...draft, mcpServerIds: [] },
  })
  const { mounted, dirty } = useFormLifetime(form.formState.isDirty || avatar.pending !== null || draftDirty)

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
  async function submit(values: PersonalAgentFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return
    try {
      await createPersonalAgent({ ...personalAgentInput(values, avatarFileId), computerId: computerID })
      void invalidate()
      if (!mounted.current) return
      toast.success(t("form.created"))
      dirty.current = false
      form.reset(values)
      avatar.clear()
      onSaved()
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      console.warn("创建个人 AI 员工失败", { error })
      toast.error(requestErrorMessage(error, personalAgentErrorFields))
    }
  }

  return (
    <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <PersonalAgentFields
        control={form.control}
        disabled={form.formState.isSubmitting}
        avatarURL={avatar.pending?.previewURL}
        avatarLoading={avatar.pending?.status === "uploading"}
        onAvatarSelect={avatar.select}
        computerName={computerName}
        leading={
          <AgentServiceAudiencesField
            value={[]}
            onChange={(audiences) => switchToService(audiences)}
            onBlur={() => undefined}
            disabled={form.formState.isSubmitting}
            personal={{ checked: true, available: true, onChange: (checked) => !checked && switchToService([]) }}
          />
        }
      />
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
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
  const navigate = useNavigate()
  const schema = usePersonalAgentSchema()
  const { personalAgent: agent, execution } = detail
  const values = useMemo<PersonalAgentFormValues>(
    () => ({
      displayName: agent.displayName,
      modelId: execution.managed.model.id,
      systemInstruction: execution.managed.systemInstruction,
      knowledgeBaseIds: execution.managed.knowledgeBaseIds,
      mcpServerIds: execution.mcpServerIds,
    }),
    [agent.displayName, execution],
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
      if (recoverSession(error, navigate)) return false
      console.warn("保存个人 AI 员工失败", { agent_id: agent.id, error })
      toast.error(requestErrorMessage(error, personalAgentErrorFields))
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
      />
    </form>
  )
}

/** 渲染个人 AI 员工的头像、名称、对话模型、指令、知识库、企业 MCP 服务与只读的使用电脑。 */
function PersonalAgentFields({
  control,
  disabled,
  avatarURL,
  avatarLoading,
  onAvatarSelect,
  computerName,
  leading,
}: {
  control: Control<PersonalAgentFormValues>
  disabled: boolean
  avatarURL?: string
  avatarLoading: boolean
  onAvatarSelect: (file: File) => void
  computerName: string
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
        name="mcpServerIds"
        control={control}
        render={({ field }) => (
          <Field>
            <FieldLabel>{t("mcp.services")}</FieldLabel>
            <AgentMCPField
              value={field.value}
              onChange={field.onChange}
              disabled={disabled}
              allowCustomerScoped={false}
            />
            <FieldDescription>{t("personal.form.mcpHelp")}</FieldDescription>
          </Field>
        )}
      />
      <Field>
        <FieldLabel htmlFor="agent-computer">{t("personal.form.computer")}</FieldLabel>
        <Input id="agent-computer" value={computerName} readOnly disabled />
        <FieldDescription>{t("personal.form.computerHelp")}</FieldDescription>
      </Field>
    </FieldGroup>
  )
}
