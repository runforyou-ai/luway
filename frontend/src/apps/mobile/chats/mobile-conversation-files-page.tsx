/** 移动端会话文件子页：单聊、AI 聊天、群聊与客户会话共用的会话共享文件区。 */
import { useTranslation } from "react-i18next"
import { useParams } from "react-router"

import { MobilePageHeader } from "@/apps/mobile/shared/mobile-page"
import { ConversationFilesPanel } from "@/features/inbox/shared/conversation-files-panel"

/** 全屏展示会话共享文件区，backTo 给出返回的上级页面。 */
export function MobileConversationFilesPage({ backTo }: { backTo: (conversationID: string) => string }) {
  const { t } = useTranslation("inbox")
  const { conversationID = "" } = useParams()

  return (
    <section className="absolute inset-0 flex min-h-0 flex-col bg-background">
      <MobilePageHeader backTo={backTo(conversationID)} title={t("contextFilesTab")} />
      <div className="min-h-0 flex-1">
        <ConversationFilesPanel conversationID={conversationID} />
      </div>
    </section>
  )
}
