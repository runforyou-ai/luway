/** 移动端客户会话的业务子页：AI 客服查询业务系统的调用、参数与结果。 */
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"

import type { MobileCustomerConversationContext } from "@/apps/mobile/mobile-customer-conversation-page"
import { MobilePageHeader, MobileScrollArea } from "@/apps/mobile/mobile-page"
import { ServiceBusinessQueries } from "@/features/inbox/customer-business-queries"

/** 全屏展示当前客服周期的业务查询记录，返回时回到客户会话。 */
export function MobileCustomerBusinessPage() {
  const { t } = useTranslation("inbox")
  const { conversation } = useOutletContext<MobileCustomerConversationContext>()

  return (
    <section className="absolute inset-0 flex min-h-0 flex-col bg-background">
      <MobilePageHeader
        backTo={`/inbox/customer/${conversation.id}`}
        title={t("contextBusinessTab")}
      />
      <MobileScrollArea storageKey={`customer-business:${conversation.id}`}>
        <ServiceBusinessQueries conversationID={conversation.id} />
      </MobileScrollArea>
    </section>
  )
}
