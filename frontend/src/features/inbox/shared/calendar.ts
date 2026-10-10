/** 按日历日期将 YYYY-MM-DD 日期键回退一天。 */
export function previousDayKey(day: string) {
  const date = new Date(`${day}T00:00:00Z`)
  date.setUTCDate(date.getUTCDate() - 1)
  return date.toISOString().slice(0, 10)
}
