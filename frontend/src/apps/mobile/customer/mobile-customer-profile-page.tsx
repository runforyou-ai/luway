/** 移动端客户会话的客户资料子页：客户名称、来源渠道、分组展示的客户资料与本次访问信息，以及服务记录。 */
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"

import { ChannelType, PermissionCode, ServiceAudience, ServiceSource } from "@/api"
import type { MobileCustomerConversationContext } from "@/apps/mobile/customer/mobile-customer-conversation-page"
import {
  MobilePageHeader,
  MobileProfileField,
  MobileProfileSection,
  MobileScrollArea,
} from "@/apps/mobile/shared/mobile-page"
import { useMobilePermission } from "@/apps/mobile/shared/mobile-workspace-layout"
import { ConversationAvatar } from "@/features/inbox/shared/conversation-avatar"
import { CustomerProfileDetails } from "@/features/inbox/service/customer-profile-details"
import { CustomerServiceHistory } from "@/features/inbox/service/customer-service-history"
import { useConversationName } from "@/hooks/use-conversation-name"

/** 全屏展示客户资料，返回时回到客户会话。 */
export function MobileCustomerProfilePage() {
  const { t } = useTranslation("inbox")
  const { conversation } = useOutletContext<MobileCustomerConversationContext>()
  const conversationName = useConversationName()
  const { service } = conversation
  const canManageContacts = useMobilePermission(PermissionCode.PermissionExternalContactsManage)

  return (
    <section className="absolute inset-0 flex min-h-0 flex-col bg-background">
      <MobilePageHeader
        backTo={`/inbox/customer/${conversation.id}`}
        title={t("customerProfile")}
      />
      <MobileScrollArea
        storageKey={`customer-profile:${conversation.id}`}
        className="px-4 py-6"
      >
        <div className="flex items-center gap-3 pb-6">
          <ConversationAvatar conversation={conversation} className="size-14" />
          <div className="min-w-0">
            <h2 className="break-words text-lg font-semibold">
              {conversationName(conversation)}
            </h2>
            {service.channel ? (
              <p className="truncate text-sm text-muted-foreground">
                {service.channel.name}
              </p>
            ) : null}
          </div>
        </div>
        {/* 服务客户的渠道会话展示客户资料与访问信息，其他会话只展示服务记录。 */}
        {service.source === ServiceSource.Channel &&
        service.audience === ServiceAudience.Customer ? (
          <CustomerProfileDetails
            conversationID={conversation.id}
            website={service.channel?.type === ChannelType.Website}
            field={MobileProfileField}
            section={MobileProfileSection}
            profileReadOnly={!canManageContacts}
          />
        ) : null}
        <CustomerServiceHistory conversationID={conversation.id} />
      </MobileScrollArea>
    </section>
  )
}
