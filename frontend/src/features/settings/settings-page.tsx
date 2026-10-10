/** 设置页外壳与表单类设置项的页面；设置导航在工作台一级栏中显示。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { Identity } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ChangePasswordForm } from "@/features/settings/change-password-form"
import { CustomerServiceSettings } from "@/features/settings/customer-service-settings"
import { ComputerListPage } from "@/features/settings/computer-list-page"
import { LocalEnvironmentSettings } from "@/features/settings/local-environment-settings"
import { GeneralSettingsForm } from "@/features/settings/general-settings-form"
import { NotificationSettingsForm } from "@/features/settings/notification-settings-form"
import { ProfileSettingsForm } from "@/features/settings/profile-settings-form"
import { UserPreferencesForm } from "@/features/settings/user-preferences-form"
import { useWorkspace } from "@/contexts/workspace-context"

/** 表单类设置项的页面定义：variant 为内容区宽度变体，未给出时为表单宽度；render 按当前成员身份渲染表单。 */
type SettingsFormSectionDefinition = { variant?: "default"; render: (identity: Identity) => ReactNode }

/** 由 SettingsFormPage 渲染标题、说明与表单的设置项。 */
const formSections = {
  profile: { render: (identity: Identity) => <ProfileSettingsForm user={identity.user} /> },
  security: { render: () => <ChangePasswordForm /> },
  preferences: { render: (identity: Identity) => <UserPreferencesForm user={identity.user} /> },
  notifications: { render: (identity: Identity) => <NotificationSettingsForm user={identity.user} /> },
  computers: { variant: "default", render: () => <ComputerListPage /> },
  local: { variant: "default", render: () => <LocalEnvironmentSettings /> },
  general: { render: (identity: Identity) => <GeneralSettingsForm workspace={identity.workspace} /> },
  customerService: { render: () => <CustomerServiceSettings /> },
} satisfies Record<string, SettingsFormSectionDefinition>

/** 设置页外壳，承载独立的设置页面。 */
export function SettingsPage({ children }: { children: ReactNode }) {
  return <div className="flex min-h-0 flex-1 flex-col overflow-hidden">{children}</div>
}

/** 渲染由设置外壳给出标题、说明与表单的设置项。 */
export function SettingsFormPage({ section }: { section: keyof typeof formSections }) {
  const { t } = useTranslation("settings")
  const { identity } = useWorkspace()
  const definition: SettingsFormSectionDefinition = formSections[section]

  return (
    <SettingsPage>
      <PageHeader
        title={t(`${section}.title`)}
        description={t(`${section}.description`)}
      />
      <PageContent variant={definition.variant ?? "form"}>
        {definition.render(identity)}
      </PageContent>
    </SettingsPage>
  )
}
