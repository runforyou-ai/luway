/** 集中定义 TanStack Query 查询 key 工厂：key 首段为作用域，部署级数据不随登录变化，账号级数据在切换工作区时保留，工作区数据的 key 第二段为所属工作区编号。 */
import { partialMatchKey, type QueryKey } from "@tanstack/react-query"

import { requestWorkspace } from "@/api/session-scope"

/** 参与 key 组成的列表查询参数对象。 */
export type KeyParameters = Record<string, unknown>

/** 生成「前缀 + 可选参数」的列表 key，不带参数时作为失效用前缀。 */
export function listKey(prefix: string, parameters?: KeyParameters) {
  return parameters === undefined ? [prefix] : [prefix, parameters]
}

/** 生成「前缀 + 可选 ID」的单条数据 key，不带 ID 时作为失效用前缀。 */
export function itemKey(prefix: string, id?: string) {
  return id === undefined ? [prefix] : [prefix, id]
}

/** 生成「前缀 + 可选归属 ID + 可选参数」的子列表 key，逐级省略时作为失效用前缀。 */
export function scopedListKey(
  prefix: string,
  scopeId?: string,
  parameters?: KeyParameters,
) {
  if (scopeId === undefined) return [prefix]
  return parameters === undefined ? [prefix, scopeId] : [prefix, scopeId, parameters]
}

/** 部署级数据：与登录账号无关。 */
const deploymentKeys = {
  /** 渠道绑定链接预览。 */
  channelBindingPreview: (token: string) => ["channel-binding-preview", token],
  /** 产品文档页面正文，按语言目录与页面路径区分。 */
  productDocPage: (locale: string, path: string) => ["productDocPage", locale, path],
  /** 按令牌读取的邀请预览。 */
  invitationPreview: (token?: string) => itemKey("invitation-preview", token),
  /** 平台的安装状态与注册开关。 */
  installationStatus: () => ["installation-status"],
  /** 当前客户端连接的企业服务器地址。 */
  serverURL: () => ["server-url"],
  /** 从指定服务器准备的客户端新版本。 */
  clientUpdate: (serverURL?: string) => ["client-update", serverURL],
}

/** 账号级数据：属于登录账号，与当前工作区无关，包括平台管理。 */
const accountKeys = {
  /** 当前登录账号。 */
  account: () => ["account"],
  /** 当前账号可进入的工作区。 */
  workspaces: () => ["workspaces"],
  /** 当前账号在各工作区的提醒数量。 */
  workspaceAttention: () => ["workspace-attention"],
  /** 平台概览。 */
  platformOverview: () => ["platform-overview"],
  /** 授权状态。 */
  license: () => ["license"],
  /** 微信第三方平台配置与平台凭据状态。 */
  wechatPlatform: () => ["wechat-platform"],
  /** 平台注册策略、工作区创建策略、平台时区和运行指标上报开关。 */
  platformSettings: () => ["platform-settings"],
  /** 部署名称、部署地址、对象存储、邮件发送与部署品牌。 */
  platformDeployment: () => ["platform-deployment"],
  /** 平台账号列表，可带筛选分页参数。 */
  platformAccounts: (parameters?: KeyParameters) => listKey("platform-accounts", parameters),
  /** 平台工作区列表，可带筛选分页参数。 */
  platformWorkspaces: (parameters?: KeyParameters) => listKey("platform-workspaces", parameters),
  /** 平台整体业务使用，可带统计天数参数。 */
  platformUsage: (parameters?: KeyParameters) => listKey("platform-usage", parameters),
  /** 平台各工作区业务使用，可带统计天数、排序与分页参数。 */
  platformWorkspaceUsage: (parameters?: KeyParameters) => listKey("platform-workspace-usage", parameters),
  /** 平台运行状态。 */
  platformRuntime: () => ["platform-runtime"],
  /** 平台失败任务列表，可带分页参数。 */
  platformFailedTasks: (parameters?: KeyParameters) => listKey("platform-failed-tasks", parameters),
  /** 平台服务端日志列表，可带筛选参数。 */
  platformServerLogs: (parameters?: KeyParameters) => listKey("platform-server-logs", parameters),

}

/** 工作区数据：属于当前请求的目标工作区。 */
const workspaceKeys = {
  /** 当前工作区待接受的成员邀请。 */
  invitations: () => ["invitations"],
  /** 当前工作区的席位上限与启用的成员数。 */
  workspaceSeats: () => ["workspace-seats"],
  /** 当前账号在当前工作区中的成员身份、所属工作区和用户偏好。 */
  identity: () => ["identity"],
  /** 服务会话发起人的资料，随会话内容变化重读。 */
  requesterProfile: (conversationId?: string) => itemKey("requester-profile", conversationId),
  /** 服务会话当前周期的业务查询记录，随会话内容变化重读。 */
  serviceBusinessQueries: (conversationId?: string) => itemKey("service-business-queries", conversationId),
  /** 会话共享文件区中的文件，随文件区变化重读。 */
  conversationFiles: (conversationId?: string) => itemKey("conversation-files", conversationId),
  /** 服务会话的交接摘要与同一发起人历史周期小结，随会话内容变化重读。 */
  serviceSummaries: (conversationId?: string) => itemKey("service-summaries", conversationId),
  /** 按会话编号读取的独立摘要。 */
  conversationSummary: (conversationId?: string) => itemKey("conversation-summary", conversationId),
  /** 指定会话的列表资格。 */
  inboxConversations: (parameters?: KeyParameters) => listKey("inbox-conversations", parameters),
  /** 收件箱数据。 */
  inbox: (parameters?: KeyParameters) => listKey("inbox", parameters),
  /** 当前用户的聊天提醒未读数、待处理的服务会话数与应用角标数。 */
  inboxAttention: (parameters?: KeyParameters) => listKey("inbox-attention", parameters),
  /** 原查询位置及前后窗口大小限定的会话邻域。 */
  inboxContext: (parameters?: KeyParameters) => listKey("inbox-context", parameters),
  /** 已加载双向边界限定的完整列表窗口。 */
  inboxWindow: (parameters?: KeyParameters) => listKey("inbox-window", parameters),
  /** 收件箱检索结果，参数包含检索文本与范围。 */
  inboxSearch: (parameters?: KeyParameters) => listKey("inbox-search", parameters),
  /** 本人已归档的聊天分页列表，参数包含类型筛选与搜索词。 */
  archivedConversations: (parameters?: KeyParameters) => listKey("archived-conversations", parameters),
  /** 本机最近打开会话的摘要，参数包含会话编号和列表筛选。 */
  recentConversations: (parameters?: KeyParameters) => listKey("recent-conversations", parameters),
  /** 客服筛选候选。 */
  serviceAssignees: () => ["service-assignees"],
  /** 客服队列团队。 */
  serviceQueueTeams: () => ["service-queue-teams"],
  /** 渠道筛选候选。 */
  inboxChannels: () => ["inbox-channels"],
  /** 客服回复的译文与回译预览，参数为待翻译的回复。 */
  customerReplyTranslation: (conversationId: string, parameters?: KeyParameters) =>
    scopedListKey("customer-reply-translation", conversationId, parameters),
  /** 当前成员在客户会话中的翻译状态。 */
  conversationTranslation: (conversationId?: string) => itemKey("conversation-translation", conversationId),
  /** 一条客户会话消息面向指定语言的译文。 */
  messageTranslation: (conversationId?: string, parameters?: { messageId: string; language: string }) =>
    scopedListKey("message-translation", conversationId, parameters),
  /** 附件下载与图片读取地址。 */
  attachmentDownload: (conversationId: string, messageId?: string) => messageId === undefined
    ? ["attachment-download", conversationId] as const
    : ["attachment-download", conversationId, messageId] as const,
  /** 会话消息窗口的重读入口，参数区分同一会话的各个页面实例。 */
  conversationMessages: (conversationId?: string, parameters?: KeyParameters) =>
    scopedListKey("conversation-messages", conversationId, parameters),
  /** 成员消息最新页、前后分页与首尾游标限定的窗口范围。 */
  conversationMessagePage: (
    conversationId?: string,
    parameters?: KeyParameters,
  ) => scopedListKey("conversation-message-pages", conversationId, parameters),
  /** 目标消息上下文。 */
  conversationMessageContext: (conversationId: string, messageId?: string) =>
    scopedListKey(
      "conversation-message-context",
      conversationId,
      messageId ? { messageId } : undefined,
    ),
  /** 按运行编号读取的 AI 运行过程详情。 */
  agentRunProcess: (runId?: string) => itemKey("agent-run-process", runId),
  /** 按运行编号读取的挂起中 AI 运行已保存过程，恢复后会继续变化。 */
  agentRunWaitingProcess: (runId?: string) => itemKey("agent-run-waiting-process", runId),
  /** 按工具调用编号读取的电脑执行过程，不给出编号时匹配全部调用。 */
  agentToolCallProcess: (toolCallId?: string) => itemKey("agent-tool-call-process", toolCallId),
  /** 当前群聊的提及进度。 */
  conversationNavigation: (conversationId?: string) =>
    itemKey("conversation-navigation", conversationId),
  /** 开始一轮导航时读取的提及队列。 */
  conversationMentions: (conversationId?: string) =>
    itemKey("conversation-mentions", conversationId),
  /** 当前成员与目标身份的已有单聊。 */
  directConversation: (identityId?: string) =>
    itemKey("direct-conversation", identityId),
  /** 可用于 AI 写回复的 AI 员工。 */
  serviceReplyAgents: () => ["service-reply-agents"],
  /** 服务会话的 Copilot 线程列表。 */
  serviceCopilotThreads: (conversationId?: string) => itemKey("service-copilot-threads", conversationId),
  /** 服务会话 AI 写回复的候选，不带参数时作为该会话全部候选的失效前缀。 */
  serviceReplySuggestions: (conversationId: string, parameters?: KeyParameters) =>
    parameters === undefined
      ? ["service-reply-suggestions", conversationId]
      : ["service-reply-suggestions", conversationId, parameters],
  /** 发起内部会话时使用的成员候选项。 */
  memberOptions: () => ["member-options"],
  /** 可发起单聊的对象，含本人负责的个人 AI 员工。 */
  chatTargets: () => ["chat-targets"],
  /** 单个群聊资料和当前成员。 */
  groupConversation: (conversationId?: string) =>
    itemKey("group-conversation", conversationId),
  /** 消息渠道列表。 */
  messageChannels: () => ["message-channels"],
  messageChannelTypes: () => ["message-channel-types"],
  /** 单个消息渠道，按 ID 与类型标识，逐级省略时作为失效用前缀。 */
  messageChannel: (id?: string, type?: string) =>
    id === undefined || type === undefined
      ? itemKey("message-channel", id)
      : ["message-channel", id, type],
  /** 服务员工渠道中的外部账号及其绑定成员。 */
  channelAccounts: (channelId: string) => ["channel-accounts", channelId],
  /** 跨业务域复用的渠道选项。 */
  channelOptions: () => ["channel-options"],
  /** 渠道接待设置选项。 */
  channelReceptionOptions: () => ["channel-reception-options"],
  /** AI 模型服务商列表。 */
  aiProviders: () => ["ai-providers"],
  /** 单个 AI 模型服务商。 */
  aiProvider: (id?: string) => itemKey("ai-provider", id),
  /** 满足指定用途的可选模型，按用途区分。 */
  aiModelOptions: (usage?: string) => itemKey("ai-model-options", usage),
  /** AI 员工授权使用的业务系统摘要。 */
  agentBusinessSystemOptions: () => ["agent-business-system-options"],
  /** 业务系统列表。 */
  businessSystems: () => ["business-systems"],
  /** 单个业务系统。 */
  businessSystem: (id?: string) => itemKey("business-system", id),
  /** 当前成员已注册的个人电脑列表。 */
  computers: () => ["computers"],
  /** 当前工作区的工作区电脑列表。 */
  workspaceComputers: () => ["workspace-computers"],
  /** 本机在当前工作区的电脑注册状态。 */
  currentComputer: () => ["current-computer"],
  localEnvironment: () => ["local-environment"],
  /** 当前企业的客服工作时间。 */
  businessHours: () => ["business-hours"],
  /** 当前企业的客服超时时长。 */
  serviceTimeouts: () => ["service-timeouts"],
  /** 当前企业的会话小结设置。 */
  serviceSummarySettings: () => ["service-summary-settings"],
  /** 企业翻译设置。 */
  translationSettings: () => ["translation-settings"],
  /** 企业联网搜索设置。 */
  webSearchSettings: () => ["web-search-settings"],
  /** 当前企业的客户身份密钥。 */
  customerIdentitySecret: () => ["customer-identity-secret"],
  /** 当前企业的咨询分类目录。 */
  serviceCategories: () => ["service-categories"],
  /** 企业联系人字段定义。 */
  contactFields: () => ["contact-fields"],
  /** 企业联系人标签定义。 */
  contactTags: () => ["contact-tags"],
  /** AI 表现报表概览，参数包含统计天数、渠道与 AI 员工范围。 */
  aiPerformanceReport: (parameters?: KeyParameters) => listKey("ai-performance-report", parameters),
  /** AI 表现按维度拆分，参数包含统计天数、渠道、AI 员工范围、维度与分页。 */
  aiPerformanceBreakdowns: (parameters?: KeyParameters) => listKey("ai-performance-breakdowns", parameters),
  /** AI 表现问题会话，参数包含统计天数、渠道、AI 员工范围、问题类型与分页。 */
  aiPerformanceIssues: (parameters?: KeyParameters) => listKey("ai-performance-issues", parameters),
  /** 团队表现概览，参数包含统计天数、渠道与团队。 */
  teamPerformanceReport: (parameters?: KeyParameters) => listKey("team-performance-report", parameters),
  /** 团队表现按客服拆分，参数包含统计天数、渠道、团队与分页。 */
  teamPerformanceMembers: (parameters?: KeyParameters) => listKey("team-performance-members", parameters),
  /** 团队表现按维度拆分，参数包含统计天数、渠道、团队、维度与分页。 */
  teamPerformanceBreakdowns: (parameters?: KeyParameters) => listKey("team-performance-breakdowns", parameters),
  /** 团队表现问题会话，参数包含统计天数、渠道、团队、问题类型与分页。 */
  teamPerformanceIssues: (parameters?: KeyParameters) => listKey("team-performance-issues", parameters),
  /** 单个问题会话的质检结论与对客沟通。 */
  serviceIssue: (serviceSessionId?: string) => itemKey("service-issue", serviceSessionId),
  /** 待补知识清单，参数包含渠道、AI 员工范围、处理状态与分页。 */
  knowledgeGaps: (parameters?: KeyParameters) => listKey("knowledge-gaps", parameters),
  /** AI 员工接待的服务记录，参数包含 AI 员工与分页。 */
  agentServiceSessions: (parameters?: KeyParameters) => listKey("agent-service-sessions", parameters),
  /** 本人负责的 AI 员工的待处理待补知识条数，位于待补知识前缀下随清单一并失效。 */
  responsibleKnowledgeGapCount: () => ["knowledge-gaps", "responsible-pending-count"],
  /** 单条待补知识详情。 */
  knowledgeGap: (id?: string) => itemKey("knowledge-gap", id),
  /** 待补知识的问题在指定知识库中召回的相似问答。 */
  knowledgeGapSimilarQA: (gapId: string, knowledgeBaseId: string, query: string) => ["knowledge-gap-similar-qa", gapId, knowledgeBaseId, query],
  /** 知识库列表。 */
  knowledgeBases: () => ["knowledge-bases"],
  /** 单个知识库。 */
  knowledgeBase: (id?: string) => itemKey("knowledge-base", id),
  /** 当前配置版本绑定指定知识库的 AI 员工。 */
  knowledgeBaseAgents: (knowledgeBaseId?: string) => itemKey("knowledge-base-agents", knowledgeBaseId),
  /** 指定知识库的问答列表。 */
  knowledgeQAEntries: (knowledgeBaseId?: string, parameters?: KeyParameters) =>
    scopedListKey("knowledge-qa-entries", knowledgeBaseId, parameters),
  /** 指定知识库中的完整问答。 */
  knowledgeQAEntry: (knowledgeBaseId: string, entryId?: string) =>
    entryId === undefined
      ? ["knowledge-qa-entry", knowledgeBaseId]
      : ["knowledge-qa-entry", knowledgeBaseId, entryId],
  /** 指定知识库的文档列表。 */
  knowledgeDocuments: (baseId?: string, parameters?: KeyParameters) => scopedListKey("knowledge-documents", baseId, parameters),
  /** 单个文档详情。 */
  knowledgeDocument: (baseId: string, documentId: string) => ["knowledge-document", baseId, documentId],
  /** 在线文档正文或网页抓取快照。 */
  knowledgeDocumentContent: (baseId: string, documentId: string) => ["knowledge-document-content", baseId, documentId],
  /** 指定文档批次和首次阅读锚点的分段列表。 */
  knowledgeDocumentSegments: (baseId: string, documentId: string, batchId: string, anchorSegmentId = "") => ["knowledge-document-segments", baseId, documentId, batchId, anchorSegmentId],
  /** 文档原件的客户端预览。 */
  knowledgeDocumentFile: (baseId: string, documentId: string) => ["knowledge-document-file", baseId, documentId],
  /** 角色列表。 */
  roles: () => ["roles"],
  /** 成员表单与筛选使用的角色选项，随角色列表一起失效。 */
  roleOptions: () => ["roles", "options"],
  /** 单个角色。 */
  role: (id?: string) => itemKey("role", id),
  /** 角色配置使用的全部真人和 AI 员工。 */
  roleMembers: () => ["role-members"],
  /** 成员列表，可带筛选分页参数。 */
  users: (parameters?: KeyParameters) => listKey("users", parameters),
  /** 全部在职成员，供选择项使用。 */
  usersAll: () => ["users", "all"],
  /** 通讯录同事目录，含成员与服务台，可带检索分页参数。 */
  colleagues: (parameters?: KeyParameters) => listKey("colleagues", parameters),
  /** 单个成员。 */
  user: (id?: string) => itemKey("user", id),
  /** 智能体列表，可带筛选分页参数。 */
  agents: (parameters?: KeyParameters) => listKey("agents", parameters),
  /** 单个智能体。 */
  agent: (id?: string) => itemKey("agent", id),
  /** AI 员工对成员公开的资料。 */
  agentProfile: (id?: string) => itemKey("agent-profile", id),
  /** AI 员工评测页的运行与用例。 */
  agentEvaluation: (agentId?: string) => itemKey("agent-evaluation", agentId),
  /** AI 员工的单条评测用例及其在最近一次运行中的尝试。 */
  agentEvaluationCase: (agentId?: string, caseId?: string) => scopedListKey("agent-evaluation-case", agentId, caseId === undefined ? undefined : { caseId }),
  /** 当前成员负责的个人 AI 员工列表。 */
  personalAgents: () => ["personal-agents"],
  /** 当前成员负责的单个个人 AI 员工。 */
  personalAgent: (id?: string) => itemKey("personal-agent", id),
  /** 待当前成员确认、审批或核对的 AI 员工操作。 */
  agentToolDecisions: () => ["agent-tool-decisions"],
  /** 个人 AI 员工的记忆列表。 */
  agentMemories: (agentId?: string) => itemKey("agent-memories", agentId),
  /** 指定成员负责的个人 AI 员工列表。 */
  memberPersonalAgents: (userId?: string) => itemKey("member-personal-agents", userId),
  /** 团队列表，可带分页参数。 */
  teams: (parameters?: KeyParameters) => listKey("teams", parameters),
  /** 单个团队。 */
  team: (id?: string) => itemKey("team", id),
  /** 团队成员列表，按团队 ID 与筛选分页参数标识。 */
  teamMembers: (teamId?: string, parameters?: KeyParameters) =>
    scopedListKey("team-members", teamId, parameters),
  /** 团队候选成员列表，按团队 ID 与筛选分页参数标识。 */
  teamMemberCandidates: (teamId?: string, parameters?: KeyParameters) =>
    scopedListKey("team-member-candidates", teamId, parameters),
  /** 外部联系人列表，可带回收站与筛选分页参数。 */
  contacts: (parameters?: KeyParameters) => listKey("contacts", parameters),
  /** 单个外部联系人。 */
  contact: (id?: string) => itemKey("contact", id),
}

/** 查询 key 的作用域。 */
export const ResourceScope = {
  Deployment: "deployment",
  Account: "account",
  Workspace: "workspace",
} as const

/** 为一组 key 工厂加上作用域前缀，prefix 在每次生成 key 时求值。 */
function scoped<T extends Record<string, (...args: never[]) => readonly unknown[]>>(prefix: () => readonly unknown[], keys: T) {
  return Object.fromEntries(
    Object.entries(keys).map(([name, key]) => [name, (...args: never[]) => [...prefix(), ...key(...args)]]),
  ) as unknown as { [K in keyof T]: (...args: Parameters<T[K]>) => unknown[] }
}

/** 按作用域定义其他模块的 key 工厂，生成的 key 与 resourceKeys 同组的 key 结构一致。 */
export const scopedResourceKeys = {
  deployment: <T extends Record<string, (...args: never[]) => readonly unknown[]>>(keys: T) => scoped(() => [ResourceScope.Deployment], keys),
  account: <T extends Record<string, (...args: never[]) => readonly unknown[]>>(keys: T) => scoped(() => [ResourceScope.Account], keys),
  workspace: <T extends Record<string, (...args: never[]) => readonly unknown[]>>(keys: T) =>
    scoped(() => [ResourceScope.Workspace, requestWorkspace()], keys),
}

export const resourceKeys = {
  ...scoped(() => [ResourceScope.Deployment], deploymentKeys),
  ...scoped(() => [ResourceScope.Account], accountKeys),
  ...scoped(() => [ResourceScope.Workspace, requestWorkspace()], workspaceKeys),
}

/** 返回去掉作用域前缀后的 key 内容，供按位置读取查询参数。 */
export function resourceKeyParts(key: QueryKey): readonly unknown[] {
  return key.slice(key[0] === ResourceScope.Workspace ? 2 : 1)
}

/** 判断 key 是否以 prefix 为前缀。 */
export function matchesResourceKey(key: QueryKey, prefix: QueryKey) {
  return partialMatchKey(key, prefix)
}
