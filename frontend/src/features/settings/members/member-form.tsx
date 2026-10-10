/** 编辑工作区成员表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  FilePurpose,
  isNotFoundApiError,
  updateUser,
  type RoleOption,
  type Team,
  type User,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { RoleSelectField } from "@/components/form/role-select-field"
import { SwitchField } from "@/components/form/switch-field"
import { TeamSelectField } from "@/components/form/team-select-field"
import { ImagePicker } from "@/components/image-picker"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { createMemberSchema, type MemberFormValues } from "@/features/settings/members/member-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 服务端字段错误可对应的表单字段。 */
const errorFields = [
  "avatarFileId",
  "displayName",
  "roleId",
  "teamIds",
  "handlesServiceRequests",
  "maxServiceSessions",
]

/** 把企业成员详情转换为编辑表单值。 */
function valuesFromUser(user: User): MemberFormValues {
  return {
    displayName: user.displayName,
    email: user.email,
    roleId: user.role.id,
    teamIds: user.teams.map((team) => team.id),
    handlesServiceRequests: user.handlesServiceRequests,
    maxServiceSessions: String(user.maxServiceSessions),
  }
}

/** 边改边存成员的头像、资料、角色、接待设置和所属团队；邮箱属于成员的账号，只读展示。 */
export function MemberForm({
  user,
  teams,
  roles,
  onSaved,
  onNotFound,
}: {
  user: User
  teams: Team[]
  roles: RoleOption[]
  onSaved: (user: User) => void
  onNotFound?: () => void
}) {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () =>
      createMemberSchema({
        nameInvalid: t("members.validation.nameInvalid"),
        roleRequired: t("members.validation.roleRequired"),
        maxServiceSessionsInvalid: t("members.validation.maxServiceSessionsInvalid"),
      }),
    [t],
  )
  const form = useForm<MemberFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 离开字段即校验以便自动保存。
    mode: "onBlur",
    defaultValues: valuesFromUser(user),
  })
  const displayName = useWatch({ control: form.control, name: "displayName" })
  const handlesServiceRequests = useWatch({ control: form.control, name: "handlesServiceRequests" })
  const avatar = usePendingImageUpload({
    purpose: FilePurpose.UserAvatar,
    onError: (error) => {
      console.warn("上传企业成员头像失败", error)
      if (!recoverSession(error, navigate)) toast.error(t("avatar.uploadError"))
    },
  })
  const { mounted, dirty, discarded } = useFormLifetime(
    form.formState.isDirty || avatar.pending !== null,
  )
  const { acceptSaved, markSaved, saveNow } = useAutoSave({
    form,
    schema,
    enabled: true,
    save: update,
    discarded,
  })

  // 成员资料刷新时同步未修改的表单和自动保存基准，保留正在编辑的草稿。
  useEffect(() => {
    if (dirty.current) return
    const values = valuesFromUser(user)
    form.reset(values)
    markSaved(values)
  }, [dirty, form, user])

  /** 上传待保存的头像后保存已有成员，返回是否保存成功。 */
  async function update(values: MemberFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return false
    try {
      const saved = await updateUser(user.id, {
        displayName: values.displayName,
        roleId: values.roleId,
        teamIds: values.teamIds,
        handlesServiceRequests: values.handlesServiceRequests,
        maxServiceSessions: Number(values.maxServiceSessions),
        avatarFileId,
      })
      onSaved(saved)
      if (!mounted.current) return true
      dirty.current = !acceptSaved(values, valuesFromUser(saved))
      avatar.clear(avatarFileId)
      return true
    } catch (error) {
      // 成员已不存在时只在表单仍打开时返回列表。
      if (isNotFoundApiError(error)) {
        if (mounted.current) onNotFound?.()
        return false
      }
      reportError(error, { log: "保存企业成员", fields: errorFields })
      return false
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form
      className="space-y-9"
      onSubmit={form.handleSubmit(() => saveNow(true))}
      noValidate
    >
      <FieldGroup className="gap-5">
        <Field>
          <FieldLabel>{t("avatar.label")}</FieldLabel>
          <ImagePicker
            imageURL={avatar.pending?.previewURL || user.avatarUrl}
            name={displayName}
            fallback="person"
            label={t("avatar.choose")}
            className="rounded-full"
            avatarClassName="rounded-full"
            disabled={isSubmitting}
            loading={avatar.pending?.status === "uploading"}
            onSelect={(file) => {
              avatar.select(file)
              saveNow(true)
            }}
          />
        </Field>
        <FormInputField
          name="displayName"
          control={form.control}
          label={t("members.form.name")}
        />
        {/* 邮箱属于成员的账号，只读展示。 */}
        <FormInputField
          name="email"
          control={form.control}
          label={t("members.form.email")}
          type="email"
          readOnly
          className="text-muted-foreground"
        />
        <Controller
          name="roleId"
          control={form.control}
          render={({ field, fieldState }) => (
            <RoleSelectField
              {...field}
              id={field.name}
              aria-invalid={fieldState.invalid}
              roles={roles}
            />
          )}
        />
        <Controller
          name="handlesServiceRequests"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id={field.name}
              name={field.name}
              label={t("members.form.handlesServiceRequests")}
              description={t("members.form.handlesServiceRequestsHelp")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        {handlesServiceRequests ? (
          <FormInputField
            name="maxServiceSessions"
            control={form.control}
            label={t("members.form.maxServiceSessions")}
            type="number"
            inputMode="numeric"
            min={1}
            step={1}
          />
        ) : null}
        <Controller
          name="teamIds"
          control={form.control}
          render={({ field }) => (
            <TeamSelectField
              teams={teams}
              label={t("members.form.teams")}
              emptyMessage={t("members.form.noTeams")}
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
            />
          )}
        />
      </FieldGroup>
    </form>
  )
}
