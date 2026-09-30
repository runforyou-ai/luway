/** 网站渠道聊天窗口页签。 */
import { useTranslation } from "react-i18next"

import {
  type WebsiteChannelChatInterfaceInput,
  type WebsiteChannelData,
  type WebsiteChannelHomeInput,
} from "@/api"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { WebsiteChannelChatInterfaceForm } from "@/features/channels/website/website-channel-chat-interface-form"
import { WebsiteChannelHelpCenterForm } from "@/features/channels/website/website-channel-help-center-form"
import { WebsiteChannelHomeForm } from "@/features/channels/website/website-channel-home-form"
import { type WebsiteHelpCenterPreviewDraft } from "@/features/channels/website/website-chat-preview"

/** 网站渠道聊天窗口子页签。 */
export type WebsiteChatInterfaceSection =
  | "appearance"
  | "conversation"
  | "home"
  | "help-center"

/** 聊天窗口子页签及其标题文案键。 */
const websiteChatInterfaceSections = {
  appearance: "chatInterface.tabs.appearance",
  conversation: "chatInterface.tabs.conversation",
  home: "chatInterface.tabs.home",
  "help-center": "chatInterface.tabs.helpCenter",
} as const satisfies Record<WebsiteChatInterfaceSection, string>

/** 按子页签展示聊天窗口外观、对话、首页与帮助中心设置。 */
export function WebsiteChannelChatInterfacePanel({
  channel,
  section,
  onSectionChange,
  onPreviewChange,
  onUpdated,
}: {
  channel: WebsiteChannelData
  section: WebsiteChatInterfaceSection
  onSectionChange: (value: WebsiteChatInterfaceSection) => void
  onPreviewChange: (
    value:
      | WebsiteChannelChatInterfaceInput
      | WebsiteChannelHomeInput
      | WebsiteHelpCenterPreviewDraft,
  ) => void
  onUpdated: () => void
}) {
  const { t } = useTranslation("channels")

  return (
    <Tabs
      value={section}
      onValueChange={(value) =>
        onSectionChange(value as WebsiteChatInterfaceSection)
      }
    >
      <TabsList>
        {Object.entries(websiteChatInterfaceSections).map(([value, key]) => (
          <TabsTrigger key={value} value={value}>
            {t(key)}
          </TabsTrigger>
        ))}
      </TabsList>
      <WebsiteChannelChatInterfaceForm
        channel={channel}
        onPreviewChange={onPreviewChange}
        onUpdated={onUpdated}
      />
      <TabsContent
        value="home"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <WebsiteChannelHomeForm
          channel={channel}
          onPreviewChange={onPreviewChange}
          onUpdated={onUpdated}
        />
      </TabsContent>
      <TabsContent
        value="help-center"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <WebsiteChannelHelpCenterForm
          channel={channel}
          onPreviewChange={onPreviewChange}
          onUpdated={onUpdated}
        />
      </TabsContent>
    </Tabs>
  )
}
