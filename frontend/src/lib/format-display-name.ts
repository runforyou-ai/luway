/**
 * 联系人与个人 AI 员工展示名纯函数；hooks 与非 React 上下文（如系统事件文案）共用。
 */

/** 有名称时用名称，没有名称时用访客编号文案，两者都没有时返回空串。 */
export function formatContactName(
  name: string | null | undefined,
  number: number | null | undefined,
  formatVisitor: (number: number) => string,
): string {
  return name?.trim() || (number == null ? "" : formatVisitor(number))
}

/** 个人 AI 员工显示为「负责人的 AI 员工 · 名称」，其他身份原样返回名称。 */
export function formatPersonalAgentDisplayName(
  name: string,
  responsibleName: string | null | undefined,
  format: (args: { responsible: string; name: string }) => string,
): string {
  return name && responsibleName ? format({ responsible: responsibleName, name }) : name
}
