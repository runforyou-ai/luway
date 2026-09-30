/** 设置页内容；设置导航在工作台一级栏中显示。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ArchivedChatsPage } from "@/features/settings/archived-chats-page"
import { ChangePasswordForm } from "@/features/settings/change-password-form"
import { CustomerServiceSettings } from "@/features/settings/customer-service-settings"
import { DeviceListPage } from "@/features/settings/device-list-page"
import { LocalEnvironmentSettings } from "@/features/settings/local-environment-settings"
import { GeneralSettingsForm } from "@/features/settings/general-settings-form"
import { MemberListPage } from "@/features/settings/members/member-list-page"
import { NotificationSettingsForm } from "@/features/settings/notification-settings-form"
import { ProfileSettingsForm } from "@/features/settings/profile-settings-form"
import { RoleListPage } from "@/features/roles/role-list-page"
import { UserPreferencesForm } from "@/features/settings/user-preferences-form"
import { useWorkspace } from "@/contexts/workspace-context"

/** 由设置外壳直接渲染表单的设置项。 */
const formSections = [
  "profile",
  "security",
  "preferences",
  "notifications",
  "devices",
  "local",
  "general",
  "customerService",
] as const

type SettingsFormSection = (typeof formSections)[number]

type SettingsSection =
  | SettingsFormSection
  | "archivedChats"
  | "members"
  | "roles"
  | "modelServices"
  | "webSearch"

/** 判断设置项的内容是否由设置外壳内的表单渲染。 */
function isFormSection(section: SettingsSection): section is SettingsFormSection {
  return (formSections as readonly string[]).includes(section)
}

/** 渲染当前设置页面。 */
export function SettingsPage({
  section,
  children,
}: {
  section: SettingsSection
  children?: ReactNode
}) {
  const { t } = useTranslation("settings")
  const { identity } = useWorkspace()

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {children ? (
        children
      ) : isFormSection(section) ? (
        <>
          <PageHeader
            title={t(`${section}.title`)}
            description={t(`${section}.description`)}
          />
          <PageContent variant={section === "devices" || section === "local" ? "default" : "form"}>
            {section === "profile" ? (
              <ProfileSettingsForm user={identity.user} />
            ) : section === "security" ? (
              <ChangePasswordForm />
            ) : section === "devices" ? (
              <DeviceListPage />
            ) : section === "local" ? (
              <LocalEnvironmentSettings />
            ) : section === "notifications" ? (
              <NotificationSettingsForm user={identity.user} />
            ) : section === "general" ? (
              <GeneralSettingsForm organization={identity.organization} />
            ) : section === "customerService" ? (
              <CustomerServiceSettings />
            ) : (
              <UserPreferencesForm user={identity.user} />
            )}
          </PageContent>
        </>
      ) : section === "archivedChats" ? (
        <ArchivedChatsPage />
      ) : section === "members" ? (
        <MemberListPage />
      ) : (
        <RoleListPage />
      )}
    </div>
  )
}
