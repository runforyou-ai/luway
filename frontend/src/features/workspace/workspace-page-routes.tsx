/** 定义工作台页面路由。 */
import { memo, Suspense, type ReactElement } from "react"
import { LoadingIndicator } from "@/components/loading-indicator"
import { matchRoutes, useRoutes, type Location, type RouteObject } from "react-router"

import {
  AgentsModuleLayout,
  agentsModulePaths,
} from "@/features/agents/agents-module-layout"
import { PermissionCode, type CurrentUser } from "@/api"
import { appExtensions } from "@/app-extensions"
import { lazyNamed } from "@/lib/lazy-named"
import { hasPermission } from "@/lib/permissions"
import { resolveAppPlatform } from "@/platform/app-platform"

// 页面组件在所属路由首次进入时加载。
const AgentFormPage = lazyNamed(() => import("@/features/agents/agent-form-page"), "AgentFormPage")
const AgentListPage = lazyNamed(() => import("@/features/agents/agent-list-page"), "AgentListPage")
const AIPerformancePage = lazyNamed(() => import("@/features/reports/ai-performance-page"), "AIPerformancePage")
const TeamPerformancePage = lazyNamed(() => import("@/features/reports/team-performance-page"), "TeamPerformancePage")
const MessageChannelFormPage = lazyNamed(() => import("@/features/channels/message-channel-form-page"), "MessageChannelFormPage")
const MessageChannelListPage = lazyNamed(() => import("@/features/channels/message-channel-list-page"), "MessageChannelListPage")
const PersonalAgentFormPage = lazyNamed(() => import("@/features/agents/personal/personal-agent-form-page"), "PersonalAgentFormPage")
const ContactsPage = lazyNamed(() => import("@/features/contacts/contacts-page"), "ContactsPage")
const ChatRoute = lazyNamed(() => import("@/features/inbox/chat-route"), "ChatRoute")
const InboxRoute = lazyNamed(() => import("@/features/inbox/inbox-route"), "InboxRoute")
const BusinessSystemFormPage = lazyNamed(() => import("@/features/integrations/business-systems/business-system-form-page"), "BusinessSystemFormPage")
const BusinessSystemListPage = lazyNamed(() => import("@/features/integrations/business-systems/business-system-list-page"), "BusinessSystemListPage")
const WorkspaceComputerListPage = lazyNamed(() => import("@/features/integrations/computers/workspace-computer-list-page"), "WorkspaceComputerListPage")
const ModelProviderFormPage = lazyNamed(() => import("@/features/integrations/model-services/model-provider-form-page"), "ModelProviderFormPage")
const ModelProviderListPage = lazyNamed(() => import("@/features/integrations/model-services/model-provider-list-page"), "ModelProviderListPage")
const WebSearchSettingsPage = lazyNamed(() => import("@/features/integrations/web-search/web-search-settings-page"), "WebSearchSettingsPage")
const KnowledgeDocumentListPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-document-list-page"), "KnowledgeDocumentListPage")
const KnowledgeDocumentFormPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-document-form-page"), "KnowledgeDocumentFormPage")
const KnowledgeDocumentPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-document-page"), "KnowledgeDocumentPage")
const KnowledgeQAListPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-qa-list-page"), "KnowledgeQAListPage")
const KnowledgeQAFormPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-qa-form-page"), "KnowledgeQAFormPage")
const KnowledgeBaseFormPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-base-form-page"), "KnowledgeBaseFormPage")
const KnowledgeBaseListPage = lazyNamed(() => import("@/features/knowledge-base/knowledge-base-list-page"), "KnowledgeBaseListPage")
const ToolDecisionListPage = lazyNamed(() => import("@/features/tool-decisions/tool-decision-list-page"), "ToolDecisionListPage")
const RoleFormPage = lazyNamed(() => import("@/features/roles/role-form-page"), "RoleFormPage")
const MemberFormPage = lazyNamed(() => import("@/features/settings/members/member-form-page"), "MemberFormPage")
const SettingsPage = lazyNamed(() => import("@/features/settings/settings-page"), "SettingsPage")
const SettingsFormPage = lazyNamed(() => import("@/features/settings/settings-page"), "SettingsFormPage")
const ArchivedChatsPage = lazyNamed(() => import("@/features/settings/archived-chats-page"), "ArchivedChatsPage")
const MemberListPage = lazyNamed(() => import("@/features/settings/members/member-list-page"), "MemberListPage")
const RoleListPage = lazyNamed(() => import("@/features/roles/role-list-page"), "RoleListPage")
const PlatformSettingsPage = lazyNamed(() => import("@/features/platform/platform-settings-page"), "PlatformSettingsPage")

/** 需要公共外壳的路由前缀，同一前缀下的页面渲染在对应布局内。 */
const workspaceRouteLayouts = agentsModulePaths.map((prefix) => ({
  prefix,
  element: <AgentsModuleLayout />,
}))

/** 一条工作台页面路由。 */
type WorkspaceRouteDefinition = {
  path: string
  element: ReactElement
  /** 进入页面所需的权限，未声明时所有成员可进入。 */
  permission?: PermissionCode
}

/** 核心工作台路由清单。 */
const coreWorkspaceRoutes: readonly WorkspaceRouteDefinition[] = [
  { path: "/inbox", element: <InboxRoute /> },
  { path: "/chats", element: <ChatRoute /> },
  { path: "/pending", element: <ToolDecisionListPage /> },
  {
    path: "/settings/profile",
    element: <SettingsFormPage section="profile" />,
  },
  {
    path: "/settings/security",
    element: <SettingsFormPage section="security" />,
  },
  {
    path: "/settings/preferences",
    element: <SettingsFormPage section="preferences" />,
  },
  {
    path: "/settings/notifications",
    element: <SettingsFormPage section="notifications" />,
  },
  {
    path: "/settings/computers",
    element: <SettingsFormPage section="computers" />,
  },
  // 本机设置只在桌面端提供。
  ...(resolveAppPlatform() === "desktop"
    ? [
        {
          path: "/settings/local",
          element: <SettingsFormPage section="local" />,
        },
      ]
    : []),
  {
    path: "/settings/archived-chats",
    element: (
      <SettingsPage>
        <ArchivedChatsPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/general",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: <SettingsFormPage section="general" />,
  },
  {
    path: "/settings/customer-service",
    permission: PermissionCode.PermissionCustomerServiceManage,
    element: <SettingsFormPage section="customerService" />,
  },
  {
    path: "/settings/members/:userId",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <MemberFormPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/members",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <MemberListPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/roles/new",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <RoleFormPage mode="create" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/roles/:roleId",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <RoleFormPage mode="detail" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/roles",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <RoleListPage />
      </SettingsPage>
    ),
  },
  {
    path: "/contacts/employees",
    element: <ContactsPage scope="employees" />,
  },
  {
    path: "/contacts/teams",
    element: <ContactsPage scope="team" />,
  },
  {
    path: "/contacts/teams/:teamId",
    element: <ContactsPage scope="team" />,
  },
  {
    path: "/contacts/external",
    element: <ContactsPage scope="external" />,
  },
  {
    path: "/ai-performance",
    element: <AIPerformancePage />,
  },
  {
    path: "/team-performance",
    permission: PermissionCode.PermissionReportsView,
    element: <TeamPerformancePage />,
  },
  {
    path: "/ai-employees",
    element: <AgentListPage />,
  },
  {
    path: "/ai-employees/new",
    element: <AgentFormPage mode="create" />,
  },
  {
    path: "/ai-employees/personal/:agentId",
    element: <PersonalAgentFormPage />,
  },
  {
    path: "/ai-employees/:agentId",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <AgentFormPage mode="edit" />,
  },
  {
    path: "/channels",
    permission: PermissionCode.PermissionCustomerServiceManage,
    element: <MessageChannelListPage />,
  },
  {
    path: "/channels/:channelType/new",
    permission: PermissionCode.PermissionCustomerServiceManage,
    element: <MessageChannelFormPage mode="create" />,
  },
  {
    path: "/channels/:channelType/:channelId",
    permission: PermissionCode.PermissionCustomerServiceManage,
    element: <MessageChannelFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/new",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeBaseFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeBaseFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa/new",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeQAFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa/:entryId/edit",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeQAFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeQAListPage />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/new",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeDocumentFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/:documentId/edit",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeDocumentFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/:documentId",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeDocumentPage />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeDocumentListPage />,
  },
  {
    path: "/knowledge-bases",
    permission: PermissionCode.PermissionAIEmployeesManage,
    element: <KnowledgeBaseListPage />,
  },
  {
    path: "/business-systems",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: <BusinessSystemListPage />,
  },
  {
    path: "/business-systems/new",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: <BusinessSystemFormPage mode="create" />,
  },
  {
    path: "/business-systems/:businessSystemId",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: <BusinessSystemFormPage mode="edit" />,
  },
  {
    path: "/computers",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: <WorkspaceComputerListPage />,
  },
  {
    path: "/settings/model-services/new/:brand",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <ModelProviderFormPage mode="create" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/model-services/:providerId",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <ModelProviderFormPage mode="edit" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/model-services",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <ModelProviderListPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/web-search",
    permission: PermissionCode.PermissionWorkspaceManage,
    element: (
      <SettingsPage>
        <WebSearchSettingsPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/platform/overview",
    element: <PlatformSettingsPage section="overview" />,
  },
  {
    path: "/settings/platform/workspaces",
    element: <PlatformSettingsPage section="workspaces" />,
  },
  {
    path: "/settings/platform/deployment",
    element: <PlatformSettingsPage section="deployment" />,
  },
  {
    path: "/settings/platform/license",
    element: <PlatformSettingsPage section="license" />,
  },
]

/** 返回路由所属的外壳，无匹配前缀时在顶层渲染。 */
function layoutOf(path: string) {
  return workspaceRouteLayouts.find(
    (layout) => path === layout.prefix || path.startsWith(`${layout.prefix}/`),
  )
}

/** 工作台路由表：核心与构建挂接的路由清单、渲染用的路由树与按地址查找权限的平铺路由。 */
type WorkspaceRouteTables = {
  definitions: readonly WorkspaceRouteDefinition[]
  objects: RouteObject[]
  permissionRoutes: RouteObject[]
}

let routeTables: WorkspaceRouteTables | undefined

/** 返回工作台路由表，首次使用时按核心与构建挂接的路由生成；地址解析与页面渲染共用，匹配按 react-router 的路径评分决定。 */
function workspaceRoutes(): WorkspaceRouteTables {
  if (routeTables) return routeTables
  const definitions = [...coreWorkspaceRoutes, ...appExtensions().routes]
  const objects: RouteObject[] = [
    ...definitions
      .filter((definition) => !layoutOf(definition.path))
      .map((definition) => ({
        path: definition.path,
        element: definition.element,
      })),
    ...workspaceRouteLayouts.map((layout) => ({
      path: layout.prefix,
      element: layout.element,
      children: definitions
        .filter((definition) => layoutOf(definition.path) === layout)
        .map((definition) => {
          const relative = definition.path.slice(layout.prefix.length + 1)
          return {
            ...(relative ? { path: relative } : { index: true as const }),
            element: definition.element,
          }
        }),
    })),
  ]
  const permissionRoutes = definitions.map((definition) => ({
    id: definition.path,
    path: definition.path,
  }))
  routeTables = { definitions, objects, permissionRoutes }
  return routeTables
}

/** 别名地址到规范地址的跳转。 */
const workspaceRedirects: Readonly<Record<string, string>> = {
  "/settings": "/settings/profile",
  "/settings/platform": "/settings/platform/overview",
  "/contacts": "/contacts/employees",
}

type ResolvedWorkspaceLocation = {
  canonicalHref: string
  matched: boolean
  /** 地址不对应任何工作台页面。 */
  notFound: boolean
  /** 当前成员所属角色没有进入该页面的权限。 */
  forbidden: boolean
}

/** 工作台默认页面。 */
export const defaultWorkspaceHref = "/inbox"

/** 判断地址是否属于设置页。 */
export function isSettingsHref(href: string) {
  return href === "/settings" || href.startsWith("/settings/")
}

/** 把当前地址解析为规范地址；别名地址给出跳转目标，未知地址与没有权限进入的页面回到消息页。 */
export function resolveWorkspaceLocation(
  location: Pick<Location, "pathname" | "search" | "hash">,
  user: Pick<CurrentUser, "permissions">,
): ResolvedWorkspaceLocation {
  // 去掉非根路径的尾部斜杠。
  const pathname =
    location.pathname === "/"
      ? location.pathname
      : location.pathname.replace(/\/+$/, "") || "/"
  const redirectedPathname = workspaceRedirects[pathname]
  if (redirectedPathname) {
    return {
      canonicalHref: `${redirectedPathname}${location.search}${location.hash}`,
      matched: false,
      notFound: false,
      forbidden: false,
    }
  }

  const routes = workspaceRoutes()
  if (!matchRoutes(routes.objects, pathname)) {
    return { canonicalHref: defaultWorkspaceHref, matched: false, notFound: true, forbidden: false }
  }
  // 按匹配到的页面检查进入所需的权限。
  const matches = matchRoutes(routes.permissionRoutes, pathname) ?? []
  const routeId = matches[matches.length - 1]?.route.id
  const definition = routes.definitions.find((item) => item.path === routeId)
  if (!hasPermission(user, definition?.permission)) {
    return { canonicalHref: defaultWorkspaceHref, matched: false, notFound: false, forbidden: true }
  }

  return {
    canonicalHref: `${pathname}${location.search}${location.hash}`,
    matched: true,
    notFound: false,
    forbidden: false,
  }
}

/** 按指定地址渲染一份工作台页面树。 */
export const WorkspacePageRoutes = memo(function WorkspacePageRoutes({ location }: { location: string }) {
  const page = useRoutes(workspaceRoutes().objects, location)
  return (
    <Suspense fallback={<LoadingIndicator className="min-h-48 justify-center" />}>
      {page}
    </Suspense>
  )
})
