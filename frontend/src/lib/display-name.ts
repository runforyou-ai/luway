/** 企业成员与 AI 员工显示名的字符规则。 */

/** 显示名只允许字母、数字、空格和 · - _ . 符号，与服务端校验一致。 */
export const displayNamePattern = /^[\p{L}\p{M}\p{N} ·\-_.]*$/u
