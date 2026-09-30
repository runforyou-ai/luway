/** 通讯录页面协调层：按范围渲染同事、团队、外部联系人和助理面板。 */
import { lazy, Suspense, type ReactNode } from "react"
import { useParams } from "react-router"

import { LoadingIndicator } from "@/components/loading-indicator"
import { PageSplit } from "@/components/page-split"
import { AssistantsPanel } from "@/features/contacts/assistants/assistants-panel"
import { ContactScopeSidebar } from "@/features/contacts/contact-scope-sidebar"
import { MembersPanel } from "@/features/contacts/members/members-panel"
import { TeamListPanel } from "@/features/contacts/teams/team-list-panel"
import { TeamPanel } from "@/features/contacts/teams/team-panel"
import { type ContactScope } from "@/features/contacts/contact-scope"

export type { ContactScope }

// 外部联系人面板依赖电话号码库，进入该分类时再加载。
const ExternalContactsPanel = lazy(() =>
  import("@/features/contacts/external/external-contacts-panel").then((module) => ({ default: module.ExternalContactsPanel })),
)

/** 按分类列出通讯录，传入 children 时在同一布局中渲染该分类下的子页面。 */
export function ContactsPage({ scope, children }: { scope: ContactScope; children?: ReactNode }) {
  const { teamId = "" } = useParams()

  return (
    <PageSplit paneVariant="nav" pane={<ContactScopeSidebar />}>
      {children ?? (scope === "assistants" ? (
        <AssistantsPanel />
      ) : scope === "employees" ? (
        <MembersPanel />
      ) : scope === "team" && teamId ? (
        <TeamPanel teamId={teamId} />
      ) : scope === "team" ? (
        <TeamListPanel />
      ) : (
        <Suspense fallback={<LoadingIndicator className="min-h-48 justify-center" />}>
          <ExternalContactsPanel />
        </Suspense>
      ))}
    </PageSplit>
  )
}
