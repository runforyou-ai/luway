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
  PermissionLevel,
  RoleKind,
  updateRole,
  updateRoleAssignments,
  type PermissionCode,
  type PermissionDefinition,
  type PermissionResource,
  type RoleData,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { ResourceContent, resourceStatus } from "@/components/resource-content"
import { ResourceListFrame } from "@/components/resource-list"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  permissionResourceLabel,
  roleDescription,
  roleDisplayName,
} from "@/lib/role-labels"
import {
  createRoleSettingsSchema,
  roleNameMaxLength,
  type RoleSettingsFormValues,
} from "@/features/roles/role-settings-schema"
import {
  RoleMemberDialog,
  type RoleMemberChange,
} from "@/features/roles/role-member-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

const newRoleID = "new-role"

/** 按权限功能整理查看和管理选项。 */
function permissionRows(definitions: PermissionDefinition[]) {
  const rows = new Map<
    PermissionResource,
    Partial<Record<PermissionLevel, PermissionDefinition>>
  >()
  for (const definition of definitions) {
    const row = rows.get(definition.resource) ?? {}
    row[definition.level] = definition
    rows.set(definition.resource, row)
  }
  return [...rows.entries()]
}

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
  const schema = useMemo(
    () =>
      createRoleSettingsSchema({
        nameRequired: t("roles.validation.nameRequired"),
        nameTooLong: t("roles.validation.nameTooLong"),
        descriptionTooLong: t("roles.validation.descriptionTooLong"),
      }),
    [t],
  )
  const form = useForm<RoleSettingsFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验，避免原生校验把焦点锁在必填项上。
    mode: mode === "create" ? "onSubmit" : "onBlur",
    defaultValues: { name: "", description: "", permissions: [] },
  })
  const selected = useWatch({ control: form.control, name: "permissions" })
  const roleName = useWatch({ control: form.control, name: "name" })
  const admin = role?.kind === RoleKind.RoleKindAdmin
  const custom = mode === "create" || role?.kind === RoleKind.RoleKindCustom
  const memberTargetRole = useMemo<RoleData | null>(() => {
    if (role) return role
    if (mode !== "create") return null
    return {
      id: newRoleID,
      kind: RoleKind.RoleKindCustom,
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
  /** 目录和角色详情就绪后回填表单并清空成员暂存。 */
  useEffect(() => {
    if (!ready) return
    if (initializedDetail.current === mode + roleId && (form.formState.isDirty || memberChanges.length > 0)) return
    initializedDetail.current = mode + roleId
    setMemberChanges([])
    const values = {
      name: role
        ? role.kind === RoleKind.RoleKindCustom
          ? role.name
          : roleDisplayName(role, tCommon)
        : "",
      description: role ? roleDescription(role, t) : "",
      permissions: role?.permissions ?? [],
    }
    form.reset(values)
    markSaved(values)
  }, [form, ready, role, t, tCommon])

  /** 切换权限并维护管理权限对查看权限的依赖。 */
  function togglePermission(
    definition: PermissionDefinition,
    checked: boolean,
  ) {
    const next = new Set(selected)
    if (checked) {
      next.add(definition.code)
      if (definition.level === PermissionLevel.PermissionLevelManage) {
        const view = definitions.find(
          (item) =>
            item.resource === definition.resource &&
            item.level === PermissionLevel.PermissionLevelView,
        )
        if (view) next.add(view.code)
      }
    } else {
      next.delete(definition.code)
      if (definition.level === PermissionLevel.PermissionLevelView) {
        const manage = definitions.find(
          (item) =>
            item.resource === definition.resource &&
            item.level === PermissionLevel.PermissionLevelManage,
        )
        if (manage) next.delete(manage.code)
      }
    }
    form.setValue("permissions", [...next] as PermissionCode[], {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  /** 保存角色资料、权限和成员配置。 */
  // 详情页边改边存；内置管理员角色只保存成员分配，名称和权限不提交。
  const autoSave = mode === "detail"
  const { submit, saveNow, markSaved, reportError } = useFormSave({
    form,
    schema,
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
  const rows = permissionRows(definitions)
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
              </FieldGroup>

              {memberTargetRole ? (
                <section className="flex items-center justify-between gap-4 rounded-lg border bg-card p-4">
                  <div>
                    <h3 className="font-medium">{t("roles.members.sectionTitle")}</h3>
                    <p className="mt-1 text-sm text-muted-foreground">
                      {t("roles.members.count", { count: memberCount })}
                    </p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setMemberDialogOpen(true)}
                  >
                    {t("roles.members.configure")}
                  </Button>
                </section>
              ) : null}

              <section>
                <div className="mb-3">
                  <h3 className="font-medium">{t("roles.permissions.memberTitle")}</h3>
                  {admin ? (
                    <p className="mt-1 text-sm text-muted-foreground">
                      {t("roles.permissions.adminDescription")}
                    </p>
                  ) : null}
                </div>
                <ResourceListFrame>
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>{t("roles.permissions.columns.function")}</TableHead>
                        <TableHead className="w-28 text-center">
                          {t("roles.permissions.columns.view")}
                        </TableHead>
                        <TableHead className="w-28 text-center">
                          {t("roles.permissions.columns.manage")}
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map(([resource, levels]) => (
                        <TableRow key={resource}>
                          <TableCell className="font-medium">
                            {permissionResourceLabel(resource, t)}
                          </TableCell>
                          {[
                            PermissionLevel.PermissionLevelView,
                            PermissionLevel.PermissionLevelManage,
                          ].map((level) => {
                            const definition = levels[level]
                            if (!definition) {
                              return (
                                <TableCell key={level} className="text-center text-muted-foreground">
                                  —
                                </TableCell>
                              )
                            }
                            const checked = admin || selected.includes(definition.code)
                            const manage = levels[PermissionLevel.PermissionLevelManage]
                            const viewRequired =
                              level === PermissionLevel.PermissionLevelView &&
                              manage &&
                              selected.includes(manage.code)
                            return (
                              <TableCell key={level} className="text-center">
                                <input
                                  type="checkbox"
                                  className="size-4 accent-primary"
                                  checked={checked}
                                  disabled={admin || Boolean(viewRequired)}
                                  aria-label={t("roles.permissions.toggle", {
                                    resource: permissionResourceLabel(resource, t),
                                    level:
                                      level === PermissionLevel.PermissionLevelView
                                        ? t("roles.permissions.columns.view")
                                        : t("roles.permissions.columns.manage"),
                                  })}
                                  onChange={(event) =>
                                    togglePermission(definition, event.target.checked)
                                  }
                                />
                              </TableCell>
                            )
                          })}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </ResourceListFrame>
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
