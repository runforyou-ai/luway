/** 按需加载会话主区，消息正文渲染依赖随会话主区独立分包。 */
import { lazy, Suspense, type ComponentProps } from "react"
import { useTranslation } from "react-i18next"

import { LoadingIndicator } from "@/components/loading-indicator"

/** 下载会话主区模块，消息页挂载后调用以提前准备打开会话。 */
export function preloadConversationMain() {
  return import("@/features/inbox/conversation-main")
}

const ConversationMain = lazy(() =>
  preloadConversationMain().then((module) => ({ default: module.ConversationMain })),
)

/** 会话主区模块就绪前展示与会话加载一致的占位。 */
export function LazyConversationMain(props: ComponentProps<typeof ConversationMain>) {
  const { t } = useTranslation("inbox")
  return (
    <Suspense
      fallback={
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground">
          <LoadingIndicator>{t("messagesLoading")}</LoadingIndicator>
        </div>
      }
    >
      <ConversationMain {...props} />
    </Suspense>
  )
}
