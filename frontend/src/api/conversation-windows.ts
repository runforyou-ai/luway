/** 桌面端会话独立窗口调用。 */
import { OpenConversationWindow } from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import { bind } from "@/api/client"

/** 在桌面端独立窗口打开指定会话，同一会话已打开时聚焦现有窗口。 */
export const openConversationWindow = bind(OpenConversationWindow)
