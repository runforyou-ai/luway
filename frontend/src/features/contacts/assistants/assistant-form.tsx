/** 助理新建与编辑表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm, useWatch, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AgentExecutionMode,
  FilePurpose,
  createAssistant,
  updateAssistant,
  type AssistantDetailData,
  type LocalAgentKindId,
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
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { AgentKnowledgeField } from "@/components/agent-fields/agent-knowledge-field"
import { AgentMCPField } from "@/components/agent-fields/agent-mcp-field"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import {
  agentModelSelection,
  parseAgentModelSelection,
} from "@/lib/agent-model-selection"
import { useAssistantInvalidator } from "@/hooks/use-assistant-invalidator"
import { localAgentName } from "@/features/contacts/assistants/local-agent-name"
import {
  createAssistantSchema,
  type AssistantFormValues,
} from "@/features/contacts/assistants/assistant-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

const assistantErrorFields = ["displayName", "providerId", "modelIdentifier", "localAgent", "systemInstruction", "knowledgeBaseIds", "mcpServerIds"]

/** 创建助理表单的校验规则。 */
function useAssistantSchema() {
  const { t } = useTranslation("contacts")
  return useMemo(
    () =>
      createAssistantSchema({
        nameRequired: t("assistants.validation.nameRequired"),
        nameInvalid: t("assistants.validation.nameInvalid"),
        modelRequired: t("assistants.validation.modelRequired"),
        instructionTooLong: t("assistants.validation.instructionTooLong"),
      }),
    [t],
  )
}

/** 待保存头像的上传状态，上传失败时提示。 */
function useAssistantAvatar() {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  return usePendingImageUpload({
    purpose: FilePurpose.FilePurposeAgentAvatar,
    onError: (error) => {
      console.warn("上传助理头像失败", error)
      if (!recoverSession(error, navigate)) toast.error(t("avatar.uploadError"))
    },
  })
}

/** 把表单值转换为助理的资料、执行配置与企业 MCP 服务输入；由本机 Agent 完成时只提交其种类与指令。 */
function assistantInput(values: AssistantFormValues, avatarFileId: string) {
  if (values.localAgent) {
    return {
      displayName: values.displayName,
      avatarFileId,
      execution: {
        mode: AgentExecutionMode.AgentExecutionModeLocalAgent,
        localAgent: { kind: values.localAgent as LocalAgentKindId, systemInstruction: values.systemInstruction },
      },
      mcpServerIds: [],
    }
  }
  return {
    displayName: values.displayName,
    avatarFileId,
    execution: {
      mode: AgentExecutionMode.AgentExecutionModeManaged,
      managed: {
        ...parseAgentModelSelection(values.modelSelection),
        systemInstruction: values.systemInstruction,
        knowledgeBaseIds: values.knowledgeBaseIds,
      },
    },
    mcpServerIds: values.mcpServerIds,
  }
}

/** 在本机创建助理，执行电脑固定为当前电脑。 */
export function AssistantCreateForm({
  deviceID,
  deviceName,
  localAgents,
  onSaved,
  onCancel,
}: {
  deviceID: string
  deviceName: string
  localAgents: LocalAgentKindId[]
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  const invalidate = useAssistantInvalidator()
  const schema = useAssistantSchema()
  const form = useForm<AssistantFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { displayName: "", modelSelection: "", localAgent: "", systemInstruction: "", knowledgeBaseIds: [], mcpServerIds: [] },
  })
  const avatar = useAssistantAvatar()
  const { mounted, dirty } = useFormLifetime(form.formState.isDirty || avatar.pending !== null)

  /** 上传待保存的头像后创建助理。 */
  async function submit(values: AssistantFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return
    try {
      await createAssistant({ ...assistantInput(values, avatarFileId), deviceId: deviceID })
      void invalidate()
      if (!mounted.current) return
      toast.success(t("assistants.form.created"))
      dirty.current = false
      form.reset(values)
      avatar.clear()
      onSaved()
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      console.warn("创建助理失败", { error })
      toast.error(requestErrorMessage(error, assistantErrorFields))
    }
  }

  return (
    <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <AssistantFields
        control={form.control}
        disabled={form.formState.isSubmitting}
        avatarURL={avatar.pending?.previewURL}
        avatarLoading={avatar.pending?.status === "uploading"}
        onAvatarSelect={avatar.select}
        deviceName={deviceName}
        localAgents={localAgents}
        autoFocus
      />
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}

/** 编辑当前成员名下的助理，改动自动保存为新的配置版本。 */
export function AssistantEditForm({
  detail,
  onSaved,
}: {
  detail: AssistantDetailData
  onSaved: () => void
}) {
  const navigate = useNavigate()
  const schema = useAssistantSchema()
  const { assistant, execution } = detail
  const values = useMemo<AssistantFormValues>(
    () =>
      execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent
        ? {
            displayName: assistant.displayName,
            modelSelection: "",
            localAgent: execution.localAgent.kind,
            systemInstruction: execution.localAgent.systemInstruction,
            knowledgeBaseIds: [],
            mcpServerIds: [],
          }
        : {
            displayName: assistant.displayName,
            modelSelection: agentModelSelection(execution.managed.providerId, execution.managed.modelIdentifier),
            localAgent: "",
            systemInstruction: execution.managed.systemInstruction,
            knowledgeBaseIds: execution.managed.knowledgeBaseIds,
            mcpServerIds: execution.mcpServerIds,
          },
    [assistant.displayName, execution],
  )
  const form = useForm<AssistantFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: values,
  })
  const avatar = useAssistantAvatar()
  const { mounted, dirty, discarded } = useFormLifetime(form.formState.isDirty || avatar.pending !== null)

  // 详情刷新时同步未修改的表单，保留正在编辑的草稿。
  useEffect(() => {
    if (!dirty.current) form.reset(values)
  }, [values, dirty, form])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit, discarded })

  /** 提交资料、执行配置与待保存的头像。 */
  async function submit(next: AssistantFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return false
    try {
      await updateAssistant(assistant.id, assistantInput(next, avatarFileId))
      onSaved()
      if (!mounted.current) return true
      dirty.current = !acceptSaved(next)
      avatar.clear(avatarFileId)
      return true
    } catch (error) {
      if (recoverSession(error, navigate)) return false
      console.warn("保存助理失败", { assistant_id: assistant.id, error })
      toast.error(requestErrorMessage(error, assistantErrorFields))
      return false
    }
  }

  return (
    <form onSubmit={form.handleSubmit(() => saveNow(true))} noValidate>
      <AssistantFields
        control={form.control}
        disabled={form.formState.isSubmitting}
        avatarURL={avatar.pending?.previewURL || assistant.avatarUrl}
        avatarLoading={avatar.pending?.status === "uploading"}
        onAvatarSelect={(file) => {
          avatar.select(file)
          // 头像不在表单值中，选择后立即排队保存。
          saveNow(true)
        }}
        deviceName={assistant.device.name}
        localAgents={assistant.device.localAgents}
      />
    </form>
  )
}

/** 渲染助理的头像、名称、完成方式、对话模型、指令、知识库、企业 MCP 服务与只读的执行电脑；由本机 Agent 完成时不显示模型、知识库与企业 MCP 服务。 */
function AssistantFields({
  control,
  disabled,
  avatarURL,
  avatarLoading,
  onAvatarSelect,
  deviceName,
  localAgents,
  autoFocus = false,
}: {
  control: Control<AssistantFormValues>
  disabled: boolean
  avatarURL?: string
  avatarLoading: boolean
  onAvatarSelect: (file: File) => void
  deviceName: string
  localAgents: LocalAgentKindId[]
  autoFocus?: boolean
}) {
  const { t } = useTranslation(["contacts", "agents"])
  const localAgent = useWatch({ control, name: "localAgent" })
  // 已选的本机 Agent 不再可用时仍列出，便于改回由助理自己完成。
  const localAgentOptions = localAgent && !localAgents.includes(localAgent as LocalAgentKindId)
    ? [...localAgents, localAgent as LocalAgentKindId]
    : localAgents
  return (
    <FieldGroup>
      <Field>
        <FieldLabel>{t("avatar.label")}</FieldLabel>
        <ImagePicker
          imageURL={avatarURL}
          fallback="agent"
          label={t("avatar.choose")}
          disabled={disabled}
          loading={avatarLoading}
          onSelect={onAvatarSelect}
        />
      </Field>
      <FormInputField
        name="displayName"
        id="assistant-name"
        control={control}
        label={t("assistants.form.name")}
        autoFocus={autoFocus}
        disabled={disabled}
      />
      {localAgentOptions.length > 0 ? (
        <Controller
          name="localAgent"
          control={control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="assistant-executor">{t("assistants.form.executor")}</FieldLabel>
              <NativeSelect {...field} id="assistant-executor" disabled={disabled}>
                <option value="">{t("assistants.form.executorSelf")}</option>
                {localAgentOptions.map((kind) => (
                  <option key={kind} value={kind}>
                    {t("assistants.form.executorLocalAgent", { name: localAgentName(kind) })}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>{t("assistants.form.executorHelp")}</FieldDescription>
            </Field>
          )}
        />
      ) : null}
      {localAgent ? null : <AgentModelField control={control} name="modelSelection" disabled={disabled} />}
      <Controller
        name="systemInstruction"
        control={control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor="assistant-instruction">{t("assistants.form.instruction")}</FieldLabel>
            <Textarea
              {...field}
              id="assistant-instruction"
              rows={6}
              disabled={disabled}
              aria-invalid={fieldState.invalid}
            />
          </Field>
        )}
      />
      {localAgent ? null : (
        <Controller
          name="knowledgeBaseIds"
          control={control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("agents:execution.knowledgeBases")}</FieldLabel>
              <AgentKnowledgeField value={field.value} onChange={field.onChange} disabled={disabled} />
            </Field>
          )}
        />
      )}
      {localAgent ? null : (
        <Controller
          name="mcpServerIds"
          control={control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("agents:mcp.services")}</FieldLabel>
              <AgentMCPField
                value={field.value}
                onChange={field.onChange}
                disabled={disabled}
                allowCustomerScoped={false}
              />
              <FieldDescription>{t("assistants.form.mcpHelp")}</FieldDescription>
            </Field>
          )}
        />
      )}
      <Field>
        <FieldLabel htmlFor="assistant-device">{t("assistants.form.device")}</FieldLabel>
        <Input id="assistant-device" value={deviceName} readOnly disabled />
        <FieldDescription>{t("assistants.form.deviceHelp")}</FieldDescription>
      </Field>
    </FieldGroup>
  )
}
