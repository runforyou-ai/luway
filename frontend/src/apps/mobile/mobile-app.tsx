/** 移动端独立入口和路由。 */
import { lazy, useEffect } from "react"
import { Navigate, Route, Routes } from "react-router"

import { MobileChatsPage } from "@/apps/mobile/mobile-chats-page"
import { MobileInboxPage } from "@/apps/mobile/mobile-inbox-page"
import {
  MobileMePage,
  MobileMeSettingsPage,
} from "@/apps/mobile/mobile-me-page"
import { MobileContactsPage } from "@/apps/mobile/mobile-contacts-page"
import {
  MobileDetailLayout,
  MobileTabLayout,
  MobileWorkspaceLayout,
} from "@/apps/mobile/mobile-workspace-layout"
import { AccountRoutes, WorkspaceRoutes } from "@/apps/account-routes"
import { usePreventPageSelectAll } from "@/hooks/use-prevent-page-select-all"

// 底部页签之外的页面在首次进入时加载。
const MobileCreateGroupPage = lazy(() =>
  import("@/apps/mobile/mobile-create-group-page").then((module) => ({ default: module.MobileCreateGroupPage })),
)
const MobileAddGroupMembersPage = lazy(() =>
  import("@/apps/mobile/mobile-add-group-members-page").then((module) => ({ default: module.MobileAddGroupMembersPage })),
)
const MobileCustomerBusinessPage = lazy(() =>
  import("@/apps/mobile/mobile-customer-business-page").then((module) => ({ default: module.MobileCustomerBusinessPage })),
)
const MobileCustomerConversationPage = lazy(() =>
  import("@/apps/mobile/mobile-customer-conversation-page").then((module) => ({ default: module.MobileCustomerConversationPage })),
)
const MobileServiceCopilotPage = lazy(() =>
  import("@/apps/mobile/mobile-customer-copilot-page").then((module) => ({ default: module.MobileServiceCopilotPage })),
)
const MobileCustomerProfilePage = lazy(() =>
  import("@/apps/mobile/mobile-customer-profile-page").then((module) => ({ default: module.MobileCustomerProfilePage })),
)
const MobileIndividualConversationPage = lazy(() =>
  import("@/apps/mobile/mobile-individual-conversation-page").then((module) => ({ default: module.MobileIndividualConversationPage })),
)
const MobileIndividualProfilePage = lazy(() =>
  import("@/apps/mobile/mobile-individual-profile-page").then((module) => ({ default: module.MobileIndividualProfilePage })),
)
const MobileEmployeeChatPage = lazy(() =>
  import("@/apps/mobile/mobile-employee-chat-page").then((module) => ({ default: module.MobileEmployeeChatPage })),
)
const MobileEmployeeProfilePage = lazy(() =>
  import("@/apps/mobile/mobile-employee-profile-page").then((module) => ({ default: module.MobileEmployeeProfilePage })),
)
const MobileGroupConversationPage = lazy(() =>
  import("@/apps/mobile/mobile-group-conversation-page").then((module) => ({ default: module.MobileGroupConversationPage })),
)
const MobileGroupProfileEditor = lazy(() =>
  import("@/apps/mobile/mobile-group-profile-editor").then((module) => ({ default: module.MobileGroupProfileEditor })),
)
const MobileGroupMembersPage = lazy(() =>
  import("@/apps/mobile/mobile-group-members").then((module) => ({ default: module.MobileGroupMembersPage })),
)
const MobileGroupMemberActionPage = lazy(() =>
  import("@/apps/mobile/mobile-group-member-management").then((module) => ({ default: module.MobileGroupMemberActionPage })),
)
const MobileGroupDetailsPage = lazy(() =>
  import("@/apps/mobile/mobile-group-details-page").then((module) => ({ default: module.MobileGroupDetailsPage })),
)
const MobileComputersPage = lazy(() =>
  import("@/apps/mobile/mobile-computers-page").then((module) => ({ default: module.MobileComputersPage })),
)
const MobileKnowledgeGapsPage = lazy(() =>
  import("@/apps/mobile/mobile-knowledge-gaps-page").then((module) => ({ default: module.MobileKnowledgeGapsPage })),
)
const MobileKnowledgeGapReviewPage = lazy(() =>
  import("@/apps/mobile/mobile-knowledge-gaps-page").then((module) => ({ default: module.MobileKnowledgeGapReviewPage })),
)
const MobileArchivedChatsPage = lazy(() =>
  import("@/apps/mobile/mobile-archived-chats-page").then((module) => ({ default: module.MobileArchivedChatsPage })),
)
const MobileDirectoryPage = lazy(() =>
  import("@/apps/mobile/mobile-directory-page").then((module) => ({ default: module.MobileDirectoryPage })),
)
const MobileAgentConversationPage = lazy(() =>
  import("@/apps/mobile/mobile-agent-chat-page").then((module) => ({ default: module.MobileAgentConversationPage })),
)
const MobileNewChatPage = lazy(() =>
  import("@/apps/mobile/mobile-new-chat-page").then((module) => ({ default: module.MobileNewChatPage })),
)
const MobileNewChatTargetPage = lazy(() =>
  import("@/apps/mobile/mobile-new-chat-page").then((module) => ({ default: module.MobileNewChatTargetPage })),
)
const MobileInboxSearchPage = lazy(() =>
  import("@/apps/mobile/mobile-inbox-search-page").then((module) => ({ default: module.MobileInboxSearchPage })),
)
const MobileAgentMemoriesPage = lazy(() =>
  import("@/apps/mobile/mobile-agent-memories-page").then((module) => ({ default: module.MobileAgentMemoriesPage })),
)
const MobileAgentMemoryPage = lazy(() =>
  import("@/apps/mobile/mobile-agent-memories-page").then((module) => ({ default: module.MobileAgentMemoryPage })),
)
const MobilePersonalAgentEditPage = lazy(() =>
  import("@/apps/mobile/mobile-personal-agents-page").then((module) => ({ default: module.MobilePersonalAgentEditPage })),
)
const MobilePersonalAgentPage = lazy(() =>
  import("@/apps/mobile/mobile-personal-agents-page").then((module) => ({ default: module.MobilePersonalAgentPage })),
)
const MobilePersonalAgentsPage = lazy(() =>
  import("@/apps/mobile/mobile-personal-agents-page").then((module) => ({ default: module.MobilePersonalAgentsPage })),
)
const MobileCreateExternalContactPage = lazy(() =>
  import("@/apps/mobile/mobile-external-contact-editor").then((module) => ({ default: module.MobileCreateExternalContactPage })),
)
const MobileExternalContactFieldPage = lazy(() =>
  import("@/apps/mobile/mobile-external-contact-editor").then((module) => ({ default: module.MobileExternalContactFieldPage })),
)
const MobileExternalContactPage = lazy(() =>
  import("@/apps/mobile/mobile-external-contacts-page").then((module) => ({ default: module.MobileExternalContactPage })),
)
const MobileExternalContactsPage = lazy(() =>
  import("@/apps/mobile/mobile-external-contacts-page").then((module) => ({ default: module.MobileExternalContactsPage })),
)
const MobileTeamMembersPage = lazy(() =>
  import("@/apps/mobile/mobile-teams-page").then((module) => ({ default: module.MobileTeamMembersPage })),
)
const MobileTeamsPage = lazy(() =>
  import("@/apps/mobile/mobile-teams-page").then((module) => ({ default: module.MobileTeamsPage })),
)

/** 渲染移动端路由：根路径为账号级页面，工作区地址下为工作区页面。 */
export default function MobileApp({ workspaceSlug }: { workspaceSlug: string | null }) {
  usePreventPageSelectAll()

  useEffect(() => {
    /** 原生返回键优先关闭当前浮层，再交由 WebView 返回页面。 */
    function dismissOverlay(event: Event) {
      if (
        !document.querySelector(
          '[data-state="open"][role="dialog"], [data-state="open"][role="alertdialog"], [data-state="open"][role="menu"]',
        )
      )
        return
      event.preventDefault()
      document.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      )
    }
    window.addEventListener("app:back", dismissOverlay, true)
    return () => window.removeEventListener("app:back", dismissOverlay, true)
  }, [])
  if (!workspaceSlug) {
    return (
      <div className="h-dvh w-full overflow-x-hidden">
        <AccountRoutes platform="mobile" />
      </div>
    )
  }
  return (
    <div className="h-dvh w-full overflow-x-hidden">
      <WorkspaceRoutes slug={workspaceSlug}>
      <Routes>
        <Route path="/" element={<Navigate to="/inbox" replace />} />
        <Route element={<MobileWorkspaceLayout />}>
          <Route element={<MobileTabLayout />}>
            <Route path="/chats" element={<MobileChatsPage />} />
            <Route path="/inbox" element={<MobileInboxPage />} />
            <Route path="/contacts" element={<MobileContactsPage />} />
            <Route path="/me" element={<MobileMePage />} />
          </Route>
          <Route element={<MobileDetailLayout />}>
            <Route path="/search" element={<MobileInboxSearchPage />} />
            <Route
              path="/chats/group/new"
              element={<MobileCreateGroupPage />}
            />
            <Route
              path="/chats/group/:conversationID"
              element={<MobileGroupConversationPage />}
            >
              <Route path="details" element={<MobileGroupDetailsPage />}>
                <Route path="members" element={<MobileGroupMembersPage />} />
                <Route
                  path="add-members"
                  element={<MobileAddGroupMembersPage />}
                />
                <Route
                  path="remove-members"
                  element={
                    <MobileGroupMemberActionPage action="remove" />
                  }
                />
                <Route
                  path="transfer-owner"
                  element={
                    <MobileGroupMemberActionPage action="transfer" />
                  }
                />
                <Route
                  path="edit/:field"
                  element={<MobileGroupProfileEditor />}
                />
              </Route>
            </Route>
            <Route path="/chats/new" element={<MobileNewChatPage />} />
            <Route
              path="/chats/new/:identityID"
              element={<MobileNewChatTargetPage />}
            />
            <Route
              path="/chats/agent/:conversationID"
              element={<MobileAgentConversationPage />}
            >
              <Route path="profile" element={<MobileIndividualProfilePage />} />
            </Route>
            <Route
              path="/inbox/customer/:conversationID"
              element={<MobileCustomerConversationPage />}
            >
              <Route path="copilot" element={<MobileServiceCopilotPage />} />
              <Route path="profile" element={<MobileCustomerProfilePage />} />
              <Route path="business" element={<MobileCustomerBusinessPage />} />
            </Route>
            <Route
              path="/chats/direct/:conversationID"
              element={<MobileIndividualConversationPage />}
            >
              <Route path="profile" element={<MobileIndividualProfilePage />} />
            </Route>
            <Route
              path="/me/profile"
              element={<MobileMeSettingsPage section="profile" />}
            />
            <Route
              path="/me/security"
              element={<MobileMeSettingsPage section="security" />}
            />
            <Route
              path="/me/preferences"
              element={<MobileMeSettingsPage section="preferences" />}
            />
            <Route
              path="/me/notifications"
              element={<MobileMeSettingsPage section="notifications" />}
            />
            <Route path="/me/computers" element={<MobileComputersPage />} />
            <Route path="/me/archived-chats" element={<MobileArchivedChatsPage />} />
            <Route path="/me/knowledge-gaps" element={<MobileKnowledgeGapsPage />} />
            <Route path="/me/knowledge-gaps/review" element={<MobileKnowledgeGapReviewPage />} />
            <Route path="/contacts/employees" element={<MobileDirectoryPage />} />
            <Route
              path="/contacts/employees/:userID"
              element={<MobileEmployeeProfilePage />}
            />
            <Route
              path="/contacts/employees/:userID/chat"
              element={<MobileEmployeeChatPage />}
            />
            <Route
              path="/me/personal-agents"
              element={<MobilePersonalAgentsPage />}
            />
            <Route
              path="/me/personal-agents/:agentID"
              element={<MobilePersonalAgentPage />}
            />
            <Route
              path="/me/personal-agents/:agentID/edit"
              element={<MobilePersonalAgentEditPage />}
            />
            <Route
              path="/me/personal-agents/:agentID/memories"
              element={<MobileAgentMemoriesPage />}
            />
            <Route
              path="/me/personal-agents/:agentID/memories/:memoryID"
              element={<MobileAgentMemoryPage />}
            />
            <Route path="/contacts/teams" element={<MobileTeamsPage />} />
            <Route
              path="/contacts/teams/:teamID"
              element={<MobileTeamMembersPage />}
            />
            <Route
              path="/contacts/external"
              element={<MobileExternalContactsPage />}
            />
            <Route
              path="/contacts/external/new"
              element={<MobileCreateExternalContactPage />}
            />
            <Route
              path="/contacts/external/:contactID"
              element={<MobileExternalContactPage />}
            />
            <Route
              path="/contacts/external/:contactID/edit/:field"
              element={<MobileExternalContactFieldPage />}
            />
          </Route>
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      </WorkspaceRoutes>
    </div>
  )
}
