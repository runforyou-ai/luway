/** 客户会话翻译调用：翻译状态、消息译文、回复语言与回复译文预览。 */
import {
  GetConversationTranslation,
  PreviewCustomerReplyTranslation,
  TranslateConversationMessages,
  UpdateCustomerReplyLanguage,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type { ConversationMessageTranslationResult } from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"

/** 读取当前成员在客户会话中的翻译状态。 */
export const getConversationTranslation = bind(GetConversationTranslation)

/** 锁定或解除客户会话的对客回复语言，空字符串表示跟随客户最近消息的语言。 */
export const updateCustomerReplyLanguage = bind(UpdateCustomerReplyLanguage)

/** 把客服回复译为客户语言并回译为客服语言。 */
export const previewCustomerReplyTranslation = bind(PreviewCustomerReplyTranslation)

const translateConversationMessagesBound = bind(TranslateConversationMessages)

/** 同一批次合并的等待时长与消息数量上限。 */
const translationBatchDelay = 120
const translationBatchLimit = 20

type PendingTranslation = {
  messageID: string
  resolve: (result: ConversationMessageTranslationResult | null) => void
  reject: (error: unknown) => void
}

const pendingTranslations = new Map<string, { entries: PendingTranslation[]; timer: number }>()

/** 发送一个会话已排队的翻译请求，按消息分发结果；不可翻译的消息得到 null。 */
function flushTranslations(conversationID: string) {
  const batch = pendingTranslations.get(conversationID)
  if (!batch) return
  pendingTranslations.delete(conversationID)
  window.clearTimeout(batch.timer)
  const messageIds = [...new Set(batch.entries.map((entry) => entry.messageID))]
  translateConversationMessagesBound(conversationID, { messageIds })
    .then((output) => {
      const results = new Map(output.translations.map((result) => [result.messageId, result]))
      for (const entry of batch.entries) entry.resolve(results.get(entry.messageID) ?? null)
    })
    .catch((error: unknown) => {
      for (const entry of batch.entries) entry.reject(error)
    })
}

/** 读取一条消息面向当前成员语言的译文；同一会话短时间内的请求合并为一次调用。 */
export function translateConversationMessage(conversationID: string, messageID: string) {
  return new Promise<ConversationMessageTranslationResult | null>((resolve, reject) => {
    let batch = pendingTranslations.get(conversationID)
    if (!batch) {
      batch = { entries: [], timer: window.setTimeout(() => flushTranslations(conversationID), translationBatchDelay) }
      pendingTranslations.set(conversationID, batch)
    }
    batch.entries.push({ messageID, resolve, reject })
    if (batch.entries.length >= translationBatchLimit) flushTranslations(conversationID)
  })
}
