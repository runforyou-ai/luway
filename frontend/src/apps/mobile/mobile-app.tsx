/** 移动端独立入口和路由。 */
import { useEffect } from "react"
import { Navigate, Route, Routes } from "react-router"

import { PermissionCode } from "@/api"
import { MobileChatsPage } from "@/apps/mobile/chats/mobile-chats-page"
import { MobileInboxPage } from "@/apps/mobile/inbox/mobile-inbox-page"
import {
  MobileMePage,
  MobileMeSettingsPage,
} from "@/apps/mobile/me/mobile-me-page"
import { MobileContactsPage } from "@/apps/mobile/contacts/mobile-contacts-page"
import {
  MobileDetailLayout,
  MobilePermissionRoute,
  MobileTabLayout,
  MobileWorkspaceLayout,
} from "@/apps/mobile/shared/mobile-workspace-layout"
import { AccountRoutes, WorkspaceRoutes } from "@/apps/account-routes"
import { usePreventPageSelectAll } from "@/hooks/use-prevent-page-select-all"
import { lazyNamed } from "@/lib/lazy-named"

// 底部页签之外的页面在首次进入时加载。
const MobileCreateGroupPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-create-group-page"), "MobileCreateGroupPage")
const MobileAddGroupMembersPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-add-group-members-page"), "MobileAddGroupMembersPage")
const MobileConversationFilesPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-conversation-files-page"), "MobileConversationFilesPage")
const MobileCustomerBusinessPage = lazyNamed(() => import("@/apps/mobile/customer/mobile-customer-business-page"), "MobileCustomerBusinessPage")
const MobileCustomerConversationPage = lazyNamed(() => import("@/apps/mobile/customer/mobile-customer-conversation-page"), "MobileCustomerConversationPage")
const MobileServiceCopilotPage = lazyNamed(() => import("@/apps/mobile/customer/mobile-customer-copilot-page"), "MobileServiceCopilotPage")
const MobileCustomerProfilePage = lazyNamed(() => import("@/apps/mobile/customer/mobile-customer-profile-page"), "MobileCustomerProfilePage")
const MobileIndividualConversationPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-individual-conversation-page"), "MobileIndividualConversationPage")
const MobileIndividualProfilePage = lazyNamed(() => import("@/apps/mobile/chats/mobile-individual-profile-page"), "MobileIndividualProfilePage")
const MobileEmployeeChatPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-employee-chat-page"), "MobileEmployeeChatPage")
const MobileEmployeeProfilePage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-employee-profile-page"), "MobileEmployeeProfilePage")
const MobileGroupConversationPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-group-conversation-page"), "MobileGroupConversationPage")
const MobileGroupProfileEditor = lazyNamed(() => import("@/apps/mobile/groups/mobile-group-profile-editor"), "MobileGroupProfileEditor")
const MobileGroupMembersPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-group-members"), "MobileGroupMembersPage")
const MobileGroupMemberActionPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-group-member-management"), "MobileGroupMemberActionPage")
const MobileGroupDetailsPage = lazyNamed(() => import("@/apps/mobile/groups/mobile-group-details-page"), "MobileGroupDetailsPage")
const MobileComputersPage = lazyNamed(() => import("@/apps/mobile/me/mobile-computers-page"), "MobileComputersPage")
const MobileKnowledgeGapsPage = lazyNamed(() => import("@/apps/mobile/me/mobile-knowledge-gaps-page"), "MobileKnowledgeGapsPage")
const MobileKnowledgeGapReviewPage = lazyNamed(() => import("@/apps/mobile/me/mobile-knowledge-gaps-page"), "MobileKnowledgeGapReviewPage")
const MobilePendingPage = lazyNamed(() => import("@/apps/mobile/me/mobile-pending-page"), "MobilePendingPage")
const MobilePendingDetailPage = lazyNamed(() => import("@/apps/mobile/me/mobile-pending-page"), "MobilePendingDetailPage")
const MobileArchivedChatsPage = lazyNamed(() => import("@/apps/mobile/me/mobile-archived-chats-page"), "MobileArchivedChatsPage")
const MobileDirectoryPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-directory-page"), "MobileDirectoryPage")
const MobileAgentConversationPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-agent-chat-page"), "MobileAgentConversationPage")
const MobileNewChatPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-new-chat-page"), "MobileNewChatPage")
const MobileNewChatTargetPage = lazyNamed(() => import("@/apps/mobile/chats/mobile-new-chat-page"), "MobileNewChatTargetPage")
const MobileInboxSearchPage = lazyNamed(() => import("@/apps/mobile/inbox/mobile-inbox-search-page"), "MobileInboxSearchPage")
const MobileAgentMemoriesPage = lazyNamed(() => import("@/apps/mobile/me/mobile-agent-memories-page"), "MobileAgentMemoriesPage")
const MobileAgentMemoryPage = lazyNamed(() => import("@/apps/mobile/me/mobile-agent-memories-page"), "MobileAgentMemoryPage")
const MobilePersonalAgentEditPage = lazyNamed(() => import("@/apps/mobile/me/mobile-personal-agents-page"), "MobilePersonalAgentEditPage")
const MobilePersonalAgentPage = lazyNamed(() => import("@/apps/mobile/me/mobile-personal-agents-page"), "MobilePersonalAgentPage")
const MobilePersonalAgentsPage = lazyNamed(() => import("@/apps/mobile/me/mobile-personal-agents-page"), "MobilePersonalAgentsPage")
const MobileCreateExternalContactPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-external-contact-editor"), "MobileCreateExternalContactPage")
const MobileExternalContactFieldPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-external-contact-editor"), "MobileExternalContactFieldPage")
const MobileExternalContactPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-external-contacts-page"), "MobileExternalContactPage")
const MobileExternalContactsPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-external-contacts-page"), "MobileExternalContactsPage")
const MobileTeamMembersPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-teams-page"), "MobileTeamMembersPage")
const MobileTeamsPage = lazyNamed(() => import("@/apps/mobile/contacts/mobile-teams-page"), "MobileTeamsPage")

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
                  path="files"
                  element={<MobileConversationFilesPage backTo={(id) => `/chats/group/${id}/details`} />}
                />
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
              <Route path="files" element={<MobileConversationFilesPage backTo={(id) => `/chats/agent/${id}`} />} />
            </Route>
            <Route
              path="/inbox/customer/:conversationID"
              element={<MobileCustomerConversationPage />}
            >
              <Route path="copilot" element={<MobileServiceCopilotPage />} />
              <Route path="profile" element={<MobileCustomerProfilePage />} />
              <Route path="business" element={<MobileCustomerBusinessPage />} />
              <Route path="files" element={<MobileConversationFilesPage backTo={(id) => `/inbox/customer/${id}`} />} />
            </Route>
            <Route
              path="/chats/direct/:conversationID"
              element={<MobileIndividualConversationPage />}
            >
              <Route path="profile" element={<MobileIndividualProfilePage />} />
              <Route path="files" element={<MobileConversationFilesPage backTo={(id) => `/chats/direct/${id}`} />} />
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
            <Route path="/me/pending" element={<MobilePendingPage />} />
            <Route path="/me/pending/detail" element={<MobilePendingDetailPage />} />
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
            {/* 外部联系人的新建与编辑需要管理权限。 */}
            <Route
              path="/contacts/external"
              element={<MobileExternalContactsPage />}
            />
            <Route
              path="/contacts/external/new"
              element={
                <MobilePermissionRoute permission={PermissionCode.PermissionExternalContactsManage}>
                  <MobileCreateExternalContactPage />
                </MobilePermissionRoute>
              }
            />
            <Route
              path="/contacts/external/:contactID"
              element={<MobileExternalContactPage />}
            />
            <Route
              path="/contacts/external/:contactID/edit/:field"
              element={
                <MobilePermissionRoute permission={PermissionCode.PermissionExternalContactsManage}>
                  <MobileExternalContactFieldPage />
                </MobilePermissionRoute>
              }
            />
          </Route>
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      </WorkspaceRoutes>
    </div>
  )
}
