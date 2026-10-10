/** 新建 AI 员工表单。 */
import { useMemo } from "react"
import { useMutation } from "@tanstack/react-query"
import { Controller, useForm, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { AgentExecutionMode, FilePurpose, createAgent, type ServiceAudience } from "@/api"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ImagePicker } from "@/components/image-picker"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { createAgentSchema, type AgentFormValues } from "@/features/agents/agent-schema"
import { AgentServiceAudiencesField } from "@/features/agents/agent-service-audiences-field"
import { useContactInvalidator } from "@/hooks/use-contact-invalidator"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** AI 员工头像的待上传状态，上传失败时提示。 */
export function useAgentAvatarUpload() {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  return usePendingImageUpload({
    purpose: FilePurpose.AgentAvatar,
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
  modelId: string
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
  const reportError = useRequestErrorReporter()
  const invalidateContact = useContactInvalidator()
  const schema = useMemo(
    () =>
      createAgentSchema({
        nameInvalid: t("validation.nameInvalid"),
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
        mode: AgentExecutionMode.Managed,
        managed: {
          modelId: draft?.modelId ?? "",
          systemInstruction: draft?.systemInstruction ?? "",
          knowledgeBaseIds: draft?.knowledgeBaseIds ?? [],
        },
      },
    },
  })
  const { dirty } = useFormLifetime(
    form.formState.isDirty || avatar.pending !== null || draftDirty,
  )
  // 头像上传失败时不创建，结果为 false。
  const creation = useMutation({
    mutationFn: async (values: AgentFormValues) => {
      const avatarFileId = await avatar.ensureUploaded()
      if (avatarFileId === null) return false
      await createAgent({
        displayName: values.displayName,
        teamIds: values.teamIds,
        serviceAudiences: values.serviceAudiences,
        avatarFileId,
        execution: {
          mode: values.execution.mode,
          managed: {
            modelId: values.execution.managed.modelId,
            systemInstruction: values.execution.managed.systemInstruction,
            knowledgeBaseIds: values.execution.managed.knowledgeBaseIds,
          },
        },
      })
      return true
    },
    onSuccess: (created) => {
      if (created) void invalidateContact("agent")
    },
  })
  const saving = form.formState.isSubmitting

  /** 上传待保存的头像后提交 AI 员工表单。 */
  function submit(values: AgentFormValues) {
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
      onError: (error) => reportError(error, {
        log: "创建 AI 员工",
        fields: [
          "displayName",
          "execution",
          "modelId",
          "systemInstruction",
          "knowledgeBaseIds",
          "teamIds",
          "serviceAudiences",
        ],
      }),
    }).catch(() => undefined)
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
              disabled={saving}
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
                      modelId: values.execution.managed.modelId,
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
            disabled={saving}
            loading={avatar.pending?.status === "uploading"}
            onSelect={avatar.select}
          />
        </Field>
        <FormInputField
          name="displayName"
          id="agent-create-name"
          control={form.control}
          label={t("form.name")}
          disabled={saving}
        />
        <AgentManagedExecutionFields
          control={form.control}
          disabled={saving}
        />
      </FieldGroup>
      <FormActions saving={saving} onCancel={onCancel} />
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
        name="execution.managed.modelId"
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
