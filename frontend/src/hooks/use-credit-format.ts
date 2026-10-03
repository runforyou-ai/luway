/** 积分数量与平台模型积分价格的展示格式。 */
import { useTranslation } from "react-i18next"

import { AIModelType, type CreditPrice } from "@/api"

/** 返回按当前语言格式化积分数量、带符号的积分变动与平台模型价格的方法。 */
export function useCreditFormat() {
  const { t, i18n } = useTranslation("common")
  const number = new Intl.NumberFormat(i18n.resolvedLanguage)
  const signed = new Intl.NumberFormat(i18n.resolvedLanguage, { signDisplay: "exceptZero" })
  return {
    /** 格式化积分数量，带单位。 */
    amount: (value: number) => t("credits.amount", { count: value, formatted: number.format(value) }),
    /** 格式化积分变动，正数带加号。 */
    change: (value: number) => signed.format(value),
    /** 按模型类型格式化价格：对话模型显示输入与输出单价，向量与重排模型显示输入单价，判断模型只按次计价，全部为 0 时显示免费。 */
    price: (price: CreditPrice, type: AIModelType) => {
      if (price.input === 0 && price.output === 0 && price.request === 0) return t("credits.free")
      const parts: string[] = []
      if (type === AIModelType.AIModelTypeChat) {
        parts.push(t("credits.tokenPrice", { input: number.format(price.input), output: number.format(price.output) }))
      } else if (type !== AIModelType.AIModelTypeDecision) {
        parts.push(t("credits.inputPrice", { input: number.format(price.input) }))
      }
      if (price.request > 0 || parts.length === 0) {
        parts.push(t("credits.requestPrice", { request: number.format(price.request) }))
      }
      return parts.join(" · ")
    },
  }
}
