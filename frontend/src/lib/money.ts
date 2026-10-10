/** 金额在币种最小单位与主单位之间的换算，以及按语言显示金额。 */

/** 返回币种主单位的小数位数，无法识别的币种按 2 位。 */
export function currencyDigits(currency: string) {
  try {
    return new Intl.NumberFormat("en", { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

/** 把以最小单位计的金额转成主单位的输入文本，如 9900 分显示为 99.00。 */
export function moneyInputValue(amount: number, currency: string) {
  const digits = currencyDigits(currency)
  return (amount / 10 ** digits).toFixed(digits)
}

/** 把主单位的金额文本解析为最小单位的整数，格式不符或小数位数超过币种允许的位数时返回 null。 */
export function parseMoney(value: string, currency: string) {
  const digits = currencyDigits(currency)
  const match = /^(\d+)(?:\.(\d+))?$/.exec(value.trim())
  if (!match || (match[2] ?? "").length > digits) return null
  return Number(match[1]) * 10 ** digits + Number((match[2] ?? "").padEnd(digits, "0") || "0")
}

/** 按语言显示以最小单位计的金额与币种符号。 */
export function formatMoney(amount: number, currency: string, locale?: string) {
  const digits = currencyDigits(currency)
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency }).format(amount / 10 ** digits)
  } catch {
    return `${(amount / 10 ** digits).toFixed(digits)} ${currency}`
  }
}
