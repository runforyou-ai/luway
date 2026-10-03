/** 定义工作台页面路由。 */
import { lazy, memo, Suspense, type ReactElement } from "react"
import { LoadingIndicator } from "@/components/loading-indicator"
import { matchRoutes, useRoutes, type Location, type RouteObject } from "react-router"

import {
  AgentsModuleLayout,
  agentsModulePaths,
} from "@/features/agents/agents-module-layout"
import { resolveAppPlatform } from "@/platform/app-platform"

// 页面组件在所属路由首次进入时加载。
const AgentFormPage = lazy(() =>
  import("@/features/agents/agent-form-page").then((module) => ({ default: module.AgentFormPage })),
)
const AgentListPage = lazy(() =>
  import("@/features/agents/agent-list-page").then((module) => ({ default: module.AgentListPage })),
)
const AIPerformancePage = lazy(() =>
  import("@/features/agents/ai-performance-page").then((module) => ({ default: module.AIPerformancePage })),
)
const TeamPerformancePage = lazy(() =>
  import("@/features/agents/team-performance-page").then((module) => ({ default: module.TeamPerformancePage })),
)
const MessageChannelFormPage = lazy(() =>
  import("@/features/channels/message-channel-form-page").then((module) => ({ default: module.MessageChannelFormPage })),
)
const MessageChannelListPage = lazy(() =>
  import("@/features/channels/message-channel-list-page").then((module) => ({ default: module.MessageChannelListPage })),
)
const PersonalAgentFormPage = lazy(() =>
  import("@/features/agents/personal/personal-agent-form-page").then((module) => ({ default: module.PersonalAgentFormPage })),
)
const ContactsPage = lazy(() =>
  import("@/features/contacts/contacts-page").then((module) => ({ default: module.ContactsPage })),
)
const ChatRoute = lazy(() =>
  import("@/features/inbox/chat-route").then((module) => ({ default: module.ChatRoute })),
)
const InboxRoute = lazy(() =>
  import("@/features/inbox/inbox-route").then((module) => ({ default: module.InboxRoute })),
)
const MCPServerFormPage = lazy(() =>
  import("@/features/integrations/mcp-servers/mcp-server-form-page").then((module) => ({ default: module.MCPServerFormPage })),
)
const MCPServerListPage = lazy(() =>
  import("@/features/integrations/mcp-servers/mcp-server-list-page").then((module) => ({ default: module.MCPServerListPage })),
)
const ModelProviderFormPage = lazy(() =>
  import("@/features/integrations/model-services/model-provider-form-page").then((module) => ({ default: module.ModelProviderFormPage })),
)
const ModelProviderListPage = lazy(() =>
  import("@/features/integrations/model-services/model-provider-list-page").then((module) => ({ default: module.ModelProviderListPage })),
)
const WebSearchSettingsPage = lazy(() =>
  import("@/features/integrations/web-search/web-search-settings-page").then((module) => ({ default: module.WebSearchSettingsPage })),
)
const KnowledgeDocumentListPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-document-list-page").then((module) => ({ default: module.KnowledgeDocumentListPage })),
)
const KnowledgeDocumentFormPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-document-form-page").then((module) => ({ default: module.KnowledgeDocumentFormPage })),
)
const KnowledgeDocumentPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-document-page").then((module) => ({ default: module.KnowledgeDocumentPage })),
)
const KnowledgeQAListPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-qa-list-page").then((module) => ({ default: module.KnowledgeQAListPage })),
)
const KnowledgeQAFormPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-qa-form-page").then((module) => ({ default: module.KnowledgeQAFormPage })),
)
const KnowledgeBaseFormPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-base-form-page").then((module) => ({ default: module.KnowledgeBaseFormPage })),
)
const KnowledgeBaseListPage = lazy(() =>
  import("@/features/knowledge-base/knowledge-base-list-page").then((module) => ({ default: module.KnowledgeBaseListPage })),
)
const RoleFormPage = lazy(() =>
  import("@/features/roles/role-form-page").then((module) => ({ default: module.RoleFormPage })),
)
const MemberFormPage = lazy(() =>
  import("@/features/settings/members/member-form-page").then((module) => ({ default: module.MemberFormPage })),
)
const SettingsPage = lazy(() =>
  import("@/features/settings/settings-page").then((module) => ({ default: module.SettingsPage })),
)
const PlatformSettingsPage = lazy(() =>
  import("@/features/settings/platform/platform-settings-page").then((module) => ({
    default: module.PlatformSettingsPage,
  })),
)

/** 需要公共外壳的路由前缀，同一前缀下的页面渲染在对应布局内。 */
const workspaceRouteLayouts = agentsModulePaths.map((prefix) => ({
  prefix,
  element: <AgentsModuleLayout />,
}))

/** 工作台路由清单，地址解析与页面渲染共用同一份定义。 */
const workspaceRouteDefinitions = [
  { path: "/inbox", element: <InboxRoute /> },
  { path: "/chats", element: <ChatRoute /> },
  {
    path: "/settings/profile",
    element: <SettingsPage section="profile" />,
  },
  {
    path: "/settings/security",
    element: <SettingsPage section="security" />,
  },
  {
    path: "/settings/preferences",
    element: <SettingsPage section="preferences" />,
  },
  {
    path: "/settings/notifications",
    element: <SettingsPage section="notifications" />,
  },
  {
    path: "/settings/computers",
    element: <SettingsPage section="computers" />,
  },
  // 本机设置只在桌面端提供。
  ...(resolveAppPlatform() === "desktop"
    ? [
        {
          path: "/settings/local",
          element: <SettingsPage section="local" />,
        },
      ]
    : []),
  {
    path: "/settings/archived-chats",
    element: <SettingsPage section="archivedChats" />,
  },
  {
    path: "/settings/general",
    element: <SettingsPage section="general" />,
  },
  {
    path: "/settings/customer-service",
    element: <SettingsPage section="customerService" />,
  },
  {
    path: "/settings/members/:userId",
    element: (
      <SettingsPage section="members">
        <MemberFormPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/members",
    element: <SettingsPage section="members" />,
  },
  {
    path: "/settings/roles/new",
    element: (
      <SettingsPage section="roles">
        <RoleFormPage mode="create" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/roles/:roleId",
    element: (
      <SettingsPage section="roles">
        <RoleFormPage mode="detail" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/roles",
    element: <SettingsPage section="roles" />,
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
    element: <AgentFormPage mode="edit" />,
  },
  {
    path: "/channels",
    element: <MessageChannelListPage />,
  },
  {
    path: "/channels/:channelType/new",
    element: <MessageChannelFormPage mode="create" />,
  },
  {
    path: "/channels/:channelType/:channelId",
    element: <MessageChannelFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/new",
    element: <KnowledgeBaseFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId",
    element: <KnowledgeBaseFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa/new",
    element: <KnowledgeQAFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa/:entryId/edit",
    element: <KnowledgeQAFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/qa",
    element: <KnowledgeQAListPage />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/new",
    element: <KnowledgeDocumentFormPage mode="create" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/:documentId/edit",
    element: <KnowledgeDocumentFormPage mode="edit" />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents/:documentId",
    element: <KnowledgeDocumentPage />,
  },
  {
    path: "/knowledge-bases/:knowledgeBaseId/documents",
    element: <KnowledgeDocumentListPage />,
  },
  {
    path: "/knowledge-bases",
    element: <KnowledgeBaseListPage />,
  },
  {
    path: "/tools",
    element: <MCPServerListPage />,
  },
  {
    path: "/tools/new",
    element: <MCPServerFormPage mode="create" />,
  },
  {
    path: "/tools/:mcpServerId",
    element: <MCPServerFormPage mode="edit" />,
  },
  {
    path: "/settings/model-services/new/:brand",
    element: (
      <SettingsPage section="modelServices">
        <ModelProviderFormPage mode="create" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/model-services/:providerId",
    element: (
      <SettingsPage section="modelServices">
        <ModelProviderFormPage mode="edit" />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/model-services",
    element: (
      <SettingsPage section="modelServices">
        <ModelProviderListPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/credits",
    element: <SettingsPage section="credits" />,
  },
  {
    path: "/settings/web-search",
    element: (
      <SettingsPage section="webSearch">
        <WebSearchSettingsPage />
      </SettingsPage>
    ),
  },
  {
    path: "/settings/platform/overview",
    element: <PlatformSettingsPage section="overview" />,
  },
  {
    path: "/settings/platform/usage",
    element: <PlatformSettingsPage section="usage" />,
  },
  {
    path: "/settings/platform/runtime",
    element: <PlatformSettingsPage section="runtime" />,
  },
  {
    path: "/settings/platform/license",
    element: <PlatformSettingsPage section="license" />,
  },
  {
    path: "/settings/platform/accounts",
    element: <PlatformSettingsPage section="accounts" />,
  },
  {
    path: "/settings/platform/workspaces",
    element: <PlatformSettingsPage section="workspaces" />,
  },
  {
    path: "/settings/platform/registration",
    element: <PlatformSettingsPage section="registration" />,
  },
  {
    path: "/settings/platform/models",
    element: <PlatformSettingsPage section="platformModels" />,
  },
  {
    path: "/settings/platform/models/new",
    element: <PlatformSettingsPage section="platformModelCreate" />,
  },
  {
    path: "/settings/platform/models/:modelId",
    element: <PlatformSettingsPage section="platformModelEdit" />,
  },
  {
    path: "/settings/platform/providers",
    element: <PlatformSettingsPage section="platformProviders" />,
  },
  {
    path: "/settings/platform/providers/new/:brand",
    element: <PlatformSettingsPage section="platformProviderCreate" />,
  },
  {
    path: "/settings/platform/providers/:providerId",
    element: <PlatformSettingsPage section="platformProviderEdit" />,
  },
  {
    path: "/settings/platform/model-calls",
    element: <PlatformSettingsPage section="platformCalls" />,
  },
  {
    path: "/settings/platform/credits",
    element: <PlatformSettingsPage section="credits" />,
  },
] as const satisfies readonly {
  path: string
  element: ReactElement
}[]

/** 返回路由所属的外壳，无匹配前缀时在顶层渲染。 */
function layoutOf(path: string) {
  return workspaceRouteLayouts.find(
    (layout) => path === layout.prefix || path.startsWith(`${layout.prefix}/`),
  )
}

/**
 * 由清单生成的路由树，地址解析与页面渲染共用。
 * 匹配按 react-router 的路径评分决定，与清单顺序无关。
 */
const workspaceRouteObjects: RouteObject[] = [
  ...workspaceRouteDefinitions
    .filter((definition) => !layoutOf(definition.path))
    .map((definition) => ({
      path: definition.path,
      element: definition.element,
    })),
  ...workspaceRouteLayouts.map((layout) => ({
    path: layout.prefix,
    element: layout.element,
    children: workspaceRouteDefinitions
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
}

export const defaultWorkspaceHref = "/inbox"

/** 判断地址是否属于设置页。 */
export function isSettingsHref(href: string) {
  return href === "/settings" || href.startsWith("/settings/")
}

/** 把当前地址解析为规范地址；别名地址给出跳转目标，未知地址回到消息页。 */
export function resolveWorkspaceLocation(
  location: Pick<Location, "pathname" | "search" | "hash">,
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
    }
  }

  if (!matchRoutes(workspaceRouteObjects, pathname)) {
    return { canonicalHref: defaultWorkspaceHref, matched: false, notFound: true }
  }

  return {
    canonicalHref: `${pathname}${location.search}${location.hash}`,
    matched: true,
    notFound: false,
  }
}

/** 按指定地址渲染一份工作台页面树。 */
export const WorkspacePageRoutes = memo(function WorkspacePageRoutes({ location }: { location: string }) {
  const page = useRoutes(workspaceRouteObjects, location)
  return (
    <Suspense fallback={<LoadingIndicator className="min-h-48 justify-center" />}>
      {page}
    </Suspense>
  )
})
