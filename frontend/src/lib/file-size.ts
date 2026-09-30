/** 格式化界面中显示的文件大小。 */

/** 使用二进制单位显示文件字节数。 */
export function formatFileSize(bytes: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"]
  const index = bytes > 0 ? Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1) : 0
  return `${Number((bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1))} ${units[index]}`
}
