/** 保存当前登录会话的移动端导航、列表加载进度、滚动位置和收件箱各页签筛选。 */
import {
  createContext,
  useContext,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import {
  isAgentInboxConversation,
  isServiceInboxConversation,
  isDirectInboxConversation,
  type InboxConversationData,
  type InboxScope,
} from "@/api"
import type { ConversationLocateTarget } from "@/features/inbox/conversation-timeline"
import type { InboxListBookmark } from "@/features/inbox/inbox-list-controller"
import type { NormalizedInboxQuery } from "@/features/inbox/inbox-query"
import { useLocation, useNavigate } from "react-router"

/** 会话详情路由携带的原消息定位目标。 */
export type MobileLocateState = { locateMessage?: ConversationLocateTarget }

type MobileNavigationState = {
  chatsURL: string
  inboxURL: string
  /** 最近一次停留的收件箱或消息列表地址。 */
  listURL: string
  scrollPositions: Map<string, number>
  listPageCounts: Map<string, number>
  inboxWindows: Map<string, InboxListBookmark>
  /** 收件箱各页签上次使用的筛选。 */
  inboxScopeQueries: Map<InboxScope, NormalizedInboxQuery>
}

const MobileNavigationContext = createContext<MobileNavigationState | null>(
  null,
)

/** 在登录工作区内保存导航状态、消息与收件箱列表的最近地址和收件箱各页签筛选，退出时一起释放。 */
export function MobileNavigationProvider({
  children,
}: {
  children: ReactNode
}) {
  const [chatsURL, setChatsURL] = useState("/chats")
  const [inboxURL, setInboxURL] = useState("/inbox")
  const [listURL, setListURL] = useState("/inbox")
  const scrollPositions = useRef(new Map<string, number>())
  const listPageCounts = useRef(new Map<string, number>())
  const inboxWindows = useRef(new Map<string, InboxListBookmark>())
  const inboxScopeQueries = useRef(new Map<InboxScope, NormalizedInboxQuery>())
  const location = useLocation()
  useLayoutEffect(() => {
    const url = location.pathname + location.search
    if (location.pathname === "/chats") {
      setChatsURL(url)
      setListURL(url)
    }
    if (location.pathname === "/inbox") {
      setInboxURL(url)
      setListURL(url)
    }
  }, [location.pathname, location.search])
  return (
    <MobileNavigationContext
      value={{
        chatsURL,
        inboxURL,
        listURL,
        scrollPositions: scrollPositions.current,
        listPageCounts: listPageCounts.current,
        inboxWindows: inboxWindows.current,
        inboxScopeQueries: inboxScopeQueries.current,
      }}
    >
      {children}
    </MobileNavigationContext>
  )
}

/** 返回当前登录工作区的导航记录。 */
export function useMobileNavigation() {
  const state = useContext(MobileNavigationContext)
  if (!state) throw new Error("移动端导航必须位于登录工作区内")
  return state
}

/** 沿站内来源返回，直接打开子页时回到所属模块。 */
export function useMobileBack(fallback: string) {
  const navigate = useNavigate()
  const location = useLocation()
  return () => {
    if ((location.state as { mobileBack?: boolean } | null)?.mobileBack) {
      void navigate(-1)
    } else {
      void navigate(fallback, { replace: true })
    }
  }
}

/** 返回会话摘要对应的移动端详情地址：服务会话属于收件箱，其余属于消息。 */
export function mobileConversationPath(conversation: InboxConversationData) {
  if (isServiceInboxConversation(conversation)) {
    return `/inbox/customer/${conversation.id}`
  }
  const type = isAgentInboxConversation(conversation)
    ? "agent"
    : isDirectInboxConversation(conversation)
      ? "direct"
      : "group"
  return `/chats/${type}/${conversation.id}`
}

/** 返回移动端检索页地址，传入会话编号时限定在该会话内检索。 */
export function mobileSearchPath(conversationID = "") {
  return conversationID
    ? `/search?${new URLSearchParams({ conversation: conversationID })}`
    : "/search"
}
