/** 邀请成员弹窗：填写邮箱、显示名称和角色后得到邀请链接；也用于展示重新生成的链接。 */
import { useEffect, useMemo, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import {
  RoleKind,
  createInvitation,
  type InvitationCreated,
  type RoleOption,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { RoleSelectField } from "@/components/form/role-select-field"
import { ResourceContent, type ResourceState } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { displayNamePattern } from "@/lib/display-name"
import { zodResolver } from "@/lib/zod-resolver"

/** 展示只返回一次的邀请链接并提供复制。 */
export function InvitationLink({ created }: { created: InvitationCreated }) {
  const { t } = useTranslation(["contacts", "common"])
  const { copied, copy } = useCopyFeedback<"link">()

  /** 复制邀请链接，失败时提示手动复制。 */
  async function copyLink() {
    if (!(await copy(created.link, "link"))) toast.error(t("members.invite.copyError"))
  }

  return (
    <Field>
      <FieldLabel>{t("members.invite.link")}</FieldLabel>
      <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
        <code className="flex min-h-8 min-w-0 flex-1 items-center font-mono text-xs break-all">{created.link}</code>
        <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={() => void copyLink()}>
          {copied === "link" ? t("common:actions.copied") : t("common:actions.copy")}
        </Button>
      </div>
      <FieldDescription>
        {created.emailQueued
          ? t("members.invite.linkHelpEmailQueued", { email: created.invitation.email })
          : t("members.invite.linkHelp")}
      </FieldDescription>
    </Field>
  )
}

/** 发起邀请，成功后在同一弹窗内展示邀请链接。 */
export function InviteMemberDialog({
  open,
  rolesResource,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  rolesResource: ResourceState & { data: { roles: RoleOption[] } | undefined }
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const { t } = useTranslation("contacts")
  const [created, setCreated] = useState<InvitationCreated | null>(null)

  // 关闭弹窗后下次打开重新填写。
  useEffect(() => {
    if (!open) setCreated(null)
  }, [open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{created ? t("members.invite.createdTitle") : t("members.invite.title")}</DialogTitle>
          <DialogDescription>{created ? t("members.invite.createdDescription") : t("members.invite.description")}</DialogDescription>
        </DialogHeader>
        {created ? (
          <div className="space-y-9">
            <InvitationLink created={created} />
            <div className="flex justify-end">
              <Button type="button" onClick={() => onOpenChange(false)}>
                {t("members.invite.done")}
              </Button>
            </div>
          </div>
        ) : open ? (
          <ResourceContent resources={rolesResource} errorMessage={t("members.invite.rolesLoadError")}>
            <InviteMemberForm
              roles={(rolesResource.data?.roles ?? []).filter((role) => role.assignable)}
              onCancel={() => onOpenChange(false)}
              onInvited={onCreated}
              onCreated={setCreated}
            />
          </ResourceContent>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/** 邀请表单，只提供当前成员可以分配的角色；显示名称选填，为空时使用受邀人的账号名称。 */
function InviteMemberForm({
  roles,
  onCancel,
  onInvited,
  onCreated,
}: {
  roles: RoleOption[]
  onCancel: () => void
  onInvited: () => void
  onCreated: (created: InvitationCreated) => void
}) {
  const { t } = useTranslation(["contacts", "common"])
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () =>
      z.object({
        email: z.string().trim().min(1).email(),
        displayName: z
          .string()
          .trim()
          .refine((value) => value === "" || displayNamePattern.test(value), t("members.validation.nameInvalid")),
        roleId: z.string().uuid(t("members.validation.roleRequired")),
      }),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      email: "",
      displayName: "",
      roleId: roles.find((role) => role.kind === RoleKind.Member)?.id ?? roles[0]?.id ?? "",
    },
  })

  // 表单随弹窗关闭卸载，卸载后返回的链接直接丢弃，邀请列表照常刷新。
  const invitation = useMutation({
    mutationFn: (values: z.infer<typeof schema>) => createInvitation(values),
    onSuccess: () => onInvited(),
  })
  const { isSubmitting } = form.formState

  /** 提交邀请。 */
  function submit(values: z.infer<typeof schema>) {
    // 失败由 onError 提示；返回请求让提交状态覆盖整个请求。
    return invitation.mutateAsync(values, {
      onSuccess: (created) => onCreated(created),
      onError: (error) => reportError(error, { log: "创建邀请", fields: ["email", "displayName", "roleId"] }),
    }).catch(() => undefined)
  }

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField name="email" control={form.control} label={t("members.form.email")} type="email" autoFocus />
        <FormInputField name="displayName" control={form.control} label={t("members.invite.displayName")} required={false} />
        <Controller
          name="roleId"
          control={form.control}
          render={({ field, fieldState }) => (
            <RoleSelectField {...field} id={field.name} aria-invalid={fieldState.invalid} roles={roles} />
          )}
        />
      </FieldGroup>
      <div className="flex items-center justify-end gap-2">
        <Button type="button" variant="outline" disabled={isSubmitting} onClick={onCancel}>
          {t("common:actions.cancel")}
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("members.invite.submitting") : t("members.invite.submit")}
        </Button>
      </div>
    </form>
  )
}
