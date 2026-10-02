/** 新建 AI 员工表单。 */
import { useMemo } from "react"
import { Controller, useForm, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AgentExecutionMode,
  FilePurpose,
  createAgent,
  type ServiceAudience,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { ImagePicker } from "@/components/image-picker"
import { FormActions } from "@/components/form/form-actions"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import { parseAgentModelSelection } from "@/lib/agent-model-selection"
import {
  createAgentSchema,
  type AgentFormValues,
} from "@/features/agents/agent-schema"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import { useContactInvalidator } from "@/hooks/use-contact-invalidator"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** AI 员工头像的待上传状态，上传失败时提示。 */
export function useAgentAvatarUpload() {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  return usePendingImageUpload({
    purpose: FilePurpose.FilePurposeAgentAvatar,
    onError: (error) => {
      console.warn("上传 AI 员工头像失败", error)
      if (!recoverSession(error, navigate)) toast.error(t("avatar.uploadError"))
    },
  })
}

/** 创建页两个表单共用的头像上传状态。 */
export type AgentAvatarUpload = ReturnType<typeof useAgentAvatarUpload>

/** 创建页切换服务对象时在两个表单间保留的名称与托管执行配置。 */
export type AgentCreateDraft = {
  displayName: string
  modelSelection: string
  systemInstruction: string
  knowledgeBaseIds: string[]
}

/** 创建服务客户或员工的 AI 员工，可同时设置头像；传入 personal 时服务对象中显示「仅自己」，勾选后把已填内容交给 onSelect；draftDirty 表示从另一表单带来了未保存内容。 */
export function AgentForm({
  defaultTeamIds = [],
  defaultServiceAudiences = [],
  draft,
  draftDirty = false,
  avatar,
  personal,
  onSaved,
  onCancel,
}: {
  defaultTeamIds?: string[]
  defaultServiceAudiences?: ServiceAudience[]
  draft?: AgentCreateDraft
  draftDirty?: boolean
  avatar: AgentAvatarUpload
  personal?: { available: boolean; onSelect: (draft: AgentCreateDraft) => void }
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation(["agents", "contacts"])
  const navigate = useNavigate()
  const invalidateContact = useContactInvalidator()
  const schema = useMemo(
    () =>
      createAgentSchema({
        nameRequired: t("validation.nameRequired"),
        nameInvalid: t("validation.nameInvalid"),
        modelRequired: t("validation.modelRequired"),
        instructionTooLong: t("validation.instructionTooLong"),
      }),
    [t],
  )
  const form = useForm<AgentFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      displayName: draft?.displayName ?? "",
      teamIds: defaultTeamIds,
      serviceAudiences: defaultServiceAudiences,
      execution: {
        mode: AgentExecutionMode.AgentExecutionModeManaged,
        managed: {
          modelSelection: draft?.modelSelection ?? "",
          systemInstruction: draft?.systemInstruction ?? "",
          knowledgeBaseIds: draft?.knowledgeBaseIds ?? [],
        },
      },
    },
  })
  const { mounted, dirty } = useFormLifetime(
    form.formState.isDirty || avatar.pending !== null || draftDirty,
  )

  /** 上传待保存的头像后提交 AI 员工表单。 */
  async function submit(values: AgentFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return
    try {
      const model = parseAgentModelSelection(
        values.execution.managed.modelSelection,
      )
      await createAgent({
        displayName: values.displayName,
        teamIds: values.teamIds,
        serviceAudiences: values.serviceAudiences,
        avatarFileId,
        execution: {
          mode: values.execution.mode,
          managed: {
            ...model,
            systemInstruction: values.execution.managed.systemInstruction,
            knowledgeBaseIds: values.execution.managed.knowledgeBaseIds,
          },
        },
      })
      void invalidateContact("agent")
      if (!mounted.current) return
      toast.success(t("form.created"))
      dirty.current = false
      form.reset(values)
      avatar.clear()
      onSaved()
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      console.warn("创建 AI 员工失败", { error })
      toast.error(
        requestErrorMessage(error, [
          "displayName",
          "execution",
          "providerId",
          "modelIdentifier",
          "systemInstruction",
          "knowledgeBaseIds",
          "teamIds",
          "serviceAudiences",
        ]),
      )
    }
  }

  return (
    <form
      className="w-full space-y-9"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="serviceAudiences"
          control={form.control}
          render={({ field }) => (
            <AgentServiceAudiencesField
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              disabled={form.formState.isSubmitting}
              personal={
                personal && {
                  checked: false,
                  available: personal.available,
                  onChange: (checked) => {
                    if (!checked) return
                    // 切换为仅自己时带上已填写的名称与托管执行配置。
                    const values = form.getValues()
                    personal.onSelect({
                      displayName: values.displayName,
                      modelSelection: values.execution.managed.modelSelection,
                      systemInstruction: values.execution.managed.systemInstruction,
                      knowledgeBaseIds: values.execution.managed.knowledgeBaseIds,
                    })
                  },
                }
              }
            />
          )}
        />
        <Field>
          <FieldLabel>{t("contacts:avatar.label")}</FieldLabel>
          <ImagePicker
            imageURL={avatar.pending?.previewURL}
            fallback="agent"
            label={t("contacts:avatar.choose")}
            disabled={form.formState.isSubmitting}
            loading={avatar.pending?.status === "uploading"}
            onSelect={avatar.select}
          />
        </Field>
        <FormInputField
          name="displayName"
          id="agent-create-name"
          control={form.control}
          label={t("form.name")}
          disabled={form.formState.isSubmitting}
        />
        <AgentManagedExecutionFields
          control={form.control}
          disabled={form.formState.isSubmitting}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}

/** 渲染平台托管执行配置字段。 */
function AgentManagedExecutionFields({
  control,
  disabled,
}: {
  disabled: boolean
  control: Control<AgentFormValues>
}) {
  const { t } = useTranslation("agents")
  return (
    <>
      <AgentModelField
        control={control}
        name="execution.managed.modelSelection"
        disabled={disabled}
      />
      <Controller
        name="execution.managed.systemInstruction"
        control={control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor="agent-system-instruction">
              {t("execution.instruction")}
            </FieldLabel>
            <Textarea
              {...field}
              id="agent-system-instruction"
              rows={6}
              disabled={disabled}
              aria-invalid={fieldState.invalid}
            />
            <FieldDescription>
              {t("execution.instructionHelp")}
            </FieldDescription>
          </Field>
        )}
      />
    </>
  )
}
