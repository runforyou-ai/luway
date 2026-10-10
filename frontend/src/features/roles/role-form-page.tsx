/** 角色新建和详情页。 */
import { useRef, useEffect, useMemo, useState } from "react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  createRole,
  getRole,
  listRoles,
  RoleKind,
  updateRole,
  updateRoleAssignments,
  type PermissionCode,
  type Role,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent, resourceStatus } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { RoleMemberDialog, type RoleMemberChange } from "@/features/roles/role-member-dialog"
import {
  roleSettingsSchema,
  roleNameMaxLength,
  type RoleSettingsFormValues,
} from "@/features/roles/role-settings-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { permissionDescription, permissionLabel, roleDescription, roleDisplayName } from "@/lib/role-labels"
import { zodResolver } from "@/lib/zod-resolver"

/** 新建页成员调整中代表待创建角色的编号。 */
const newRoleID = "new-role"

/** 显示角色资料和权限表单。 */
export function RoleFormPage({ mode }: { mode: "create" | "detail" }) {
  const { t } = useTranslation("settings")
  const { t: tCommon } = useTranslation("common")
  const navigate = useNavigate()
  const { roleId = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const [memberDialogOpen, setMemberDialogOpen] = useState(false)
  const [memberChanges, setMemberChanges] = useState<RoleMemberChange[]>([])
  const catalogResource = useResource(resourceKeys.roles(), () => listRoles())
  const roleResource = useResource(
    resourceKeys.role(roleId),
    () => getRole(roleId),
    { enabled: mode === "detail" },
  )
  const roles = useMemo(
    () => catalogResource.data?.roles ?? [],
    [catalogResource.data],
  )
  const definitions = catalogResource.data?.permissions ?? []
  const role = mode === "detail" ? (roleResource.data ?? null) : null
  const resources =
    mode === "detail" ? [catalogResource, roleResource] : [catalogResource]
  const ready = resourceStatus(resources).status === "ready"
  const form = useForm<RoleSettingsFormValues>({
    resolver: zodResolver(roleSettingsSchema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存，新建时只在提交时校验。
    mode: mode === "create" ? "onSubmit" : "onBlur",
    defaultValues: { name: "", description: "", permissions: [] },
  })
  const selected = useWatch({ control: form.control, name: "permissions" })
  const roleName = useWatch({ control: form.control, name: "name" })
  const admin = role?.kind === RoleKind.Admin
  const custom = mode === "create" || role?.kind === RoleKind.Custom
  const memberTargetRole = useMemo<Role | null>(() => {
    if (role) return role
    if (mode !== "create") return null
    return {
      id: newRoleID,
      kind: RoleKind.Custom,
      name: roleName.trim() || t("roles.members.newRole"),
      description: "",
      permissions: selected,
      memberCount: 0,
      createdAt: "",
      updatedAt: "",
    }
  }, [mode, role, roleName, selected, t])
  const memberDialogRoles = useMemo(
    () =>
      memberTargetRole &&
      !roles.some((item) => item.id === memberTargetRole.id)
        ? [...roles, memberTargetRole]
        : roles,
    [memberTargetRole, roles],
  )

  const initializedDetail = useRef<string | null>(null)
  // 目录和角色详情就绪后回填表单并清空成员暂存。
  useEffect(() => {
    if (!ready) return
    if (initializedDetail.current === mode + roleId && (form.formState.isDirty || memberChanges.length > 0)) return
    initializedDetail.current = mode + roleId
    setMemberChanges([])
    const values = {
      name: role
        ? role.kind === RoleKind.Custom
          ? role.name
          : roleDisplayName(role, tCommon)
        : "",
      description: role ? roleDescription(role, t) : "",
      permissions: role?.permissions ?? [],
    }
    form.reset(values)
    markSaved(values)
  }, [form, ready, role, t, tCommon])

  /** 勾选或取消一项权限。 */
  function togglePermission(code: PermissionCode, checked: boolean) {
    const next = new Set(selected)
    if (checked) next.add(code)
    else next.delete(code)
    form.setValue("permissions", [...next], {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  // 保存角色资料、权限和成员配置：详情页边改边存；内置管理员角色只保存成员分配，名称和权限不提交。
  const autoSave = mode === "detail"
  const { submit, saveNow, markSaved, reportError } = useFormSave({
    form,
    schema: roleSettingsSchema,
    autoSave,
    save: async (values) => {
      let targetRoleID = roleId
      if (mode === "create") {
        targetRoleID = (await createRole(values)).id
        void invalidateResource(resourceKeys.roles())
      } else if (!admin) {
        await updateRole(roleId, values)
        void invalidateResource(resourceKeys.roles())
        void invalidateResource(resourceKeys.role(roleId))
      }
      const createdRoleID = mode === "create" ? targetRoleID : ""
      if (memberChanges.length > 0) {
        try {
          await updateRoleAssignments({
            assignments: memberChanges.map((change) => ({
              identityId: change.member.identityId,
              roleId:
                change.nextRoleID === newRoleID
                  ? targetRoleID
                  : change.nextRoleID,
            })),
          })
        } catch (error) {
          // 角色已创建时保留创建结果，提示成员分配失败后进入新角色详情。
          if (!createdRoleID) throw error
          return { createdRoleID, assignmentError: error }
        }
        void invalidateResource(resourceKeys.roles())
        void invalidateResource(resourceKeys.role())
        void invalidateResource(resourceKeys.users())
        void invalidateResource(resourceKeys.roleMembers())
      }
      return { createdRoleID, assignmentError: null }
    },
    onSaved: ({ assignmentError }) => {
      if (!assignmentError) setMemberChanges([])
    },
    onSubmitted: ({ createdRoleID, assignmentError }) => {
      if (assignmentError) {
        if (!reportError(assignmentError))
          navigate(`/settings/roles/${createdRoleID}`)
        return
      }
      toast.success(t("roles.form.createSuccess"))
      navigate("/settings/roles")
    },
    errorMessage: t("roles.form.saveError"),
    errorFields: ["name", "description", "permissions"],
    logLabel: "保存角色",
  })
  // 成员分配不在表单值内，变更后与自动保存串行地立即保存一次。
  useEffect(() => {
    if (!autoSave || memberChanges.length === 0) return
    saveNow(true)
  }, [autoSave, memberChanges])

  const title =
    mode === "create"
      ? t("roles.form.createTitle")
      : role
        ? roleDisplayName(role, tCommon)
        : t("roles.form.detailTitle")
  const pendingRoleIDs = useMemo(
    () =>
      Object.fromEntries(
        memberChanges.map((change) => [
          change.member.identityId,
          change.nextRoleID,
        ]),
      ),
    [memberChanges],
  )
  const memberCount = memberTargetRole
    ? memberChanges.reduce((count, change) => {
        if (change.previousRoleID === memberTargetRole.id) count -= 1
        if (change.nextRoleID === memberTargetRole.id) count += 1
        return count
      }, memberTargetRole.memberCount)
    : 0

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={title}
        description={t(
          mode === "create"
            ? "roles.form.createDescription"
            : "roles.form.detailDescription",
        )}
        backTo={mode === "detail" ? "/settings/roles" : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={resources}
          errorMessage={t("roles.form.loadError")}
        >
          <form
            className="w-full space-y-9"
            onSubmit={form.handleSubmit(submit)}
            noValidate
          >
            <div className="space-y-5">
              <FieldGroup>
                <FormInputField
                  name="name"
                  control={form.control}
                  label={t("roles.form.name")}
                  autoFocus={custom}
                  disabled={!custom}
                  maxLength={roleNameMaxLength}
                />
                <Controller
                  name="description"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor="role-description" required={false}>
                        {t("roles.form.description")}
                      </FieldLabel>
                      <Textarea
                        {...field}
                        id="role-description"
                        disabled={!custom}
                        aria-invalid={fieldState.invalid}
                      />
                    </Field>
                  )}
                />
                {memberTargetRole ? (
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldTitle>{t("roles.members.sectionTitle")}</FieldTitle>
                      <FieldDescription>
                        {t("roles.members.count", { count: memberCount })}
                      </FieldDescription>
                    </FieldContent>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => setMemberDialogOpen(true)}
                    >
                      {t("roles.members.configure")}
                    </Button>
                  </Field>
                ) : null}
              </FieldGroup>

              <section>
                <div className="mb-3">
                  <h3 className="font-medium">{t("roles.permissions.memberTitle")}</h3>
                  {admin ? (
                    <p className="mt-1 text-sm text-muted-foreground">
                      {t("roles.permissions.adminDescription")}
                    </p>
                  ) : null}
                </div>
                <div className="space-y-4">
                  {definitions.map((code) => (
                    <label key={code} className="flex items-start gap-3">
                      <input
                        type="checkbox"
                        className="mt-0.5 size-4 shrink-0 accent-primary disabled:cursor-not-allowed disabled:opacity-50"
                        checked={admin || selected.includes(code)}
                        disabled={admin}
                        onChange={(event) => togglePermission(code, event.target.checked)}
                      />
                      <span className="grid gap-1">
                        <span className="text-sm font-medium">{permissionLabel(code, t)}</span>
                        <span className="text-sm text-muted-foreground">{permissionDescription(code, t)}</span>
                      </span>
                    </label>
                  ))}
                </div>
              </section>
            </div>

            {autoSave ? null : (
              <FormActions
                saving={form.formState.isSubmitting}
                onCancel={() => navigate("/settings/roles")}
              />
            )}
          </form>
        </ResourceContent>
      </PageContent>
      <RoleMemberDialog
        role={memberDialogOpen ? memberTargetRole : null}
        roles={memberDialogRoles}
        pendingRoleIDs={pendingRoleIDs}
        onOpenChange={setMemberDialogOpen}
        onConfirm={setMemberChanges}
      />
    </div>
  )
}
