/** 校验网页地址和不含查询参数的服务端点。 */

/** 解析不含认证信息的完整 HTTP 或 HTTPS 地址。 */
function parseHTTPURL(value: string) {
  try {
    const url = new URL(value)
    if (
      (url.protocol !== "http:" && url.protocol !== "https:") ||
      url.host === "" || url.username !== "" || url.password !== ""
    ) return null
    return url
  } catch {
    return null
  }
}

/** 判断地址是否为不含认证信息的网页地址。 */
export function isHTTPURL(value: string) {
  return parseHTTPURL(value) !== null
}

/** 判断服务端点是否同时不含查询参数和片段。 */
export function isHTTPEndpoint(value: string) {
  const url = parseHTTPURL(value)
  return url !== null && url.search === "" && url.hash === ""
}

/** 判断主机是否为小写 ASCII 域名：各段 1 至 63 个字符且不以连字符开头或结尾，最后一段为数字或十六进制时须为点分十进制 IPv4。 */
function isHostName(host: string) {
  if (host === "" || host.length > 253) return false
  const labels = host.split(".")
  if (labels.some((label) => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) return false
  const last = labels[labels.length - 1]
  if (!/^\d+$/.test(last) && !/^0x[0-9a-f]*$/.test(last)) return true
  return labels.length === 4 && labels.every((label) => /^(?:0|[1-9]\d{0,2})$/.test(label) && Number(label) <= 255)
}

/** 判断地址是否为部署地址，规则与服务端一致：按小写比较，主机为 ASCII 域名、点分十进制 IPv4 或方括号内的规范 IPv6，端口为 1 至 65535 且无前导零，不带路径、查询、片段和认证信息；末尾斜杠由调用方先去掉。 */
export function isHTTPOrigin(value: string) {
  const lower = value.toLowerCase()
  const rest = lower.startsWith("https://") ? lower.slice(8) : lower.startsWith("http://") ? lower.slice(7) : null
  if (rest === null) return false
  let host = rest
  let port = ""
  if (rest.startsWith("[")) {
    const end = rest.indexOf("]")
    if (end < 0) return false
    host = rest.slice(1, end)
    port = rest.slice(end + 1)
    // IPv6 须为规范的压缩形式，且不是 IPv4 映射地址。
    let canonical = ""
    try {
      canonical = new URL(`http://[${host}]`).hostname
    } catch {
      return false
    }
    if (canonical !== `[${host}]` || /^::ffff:[0-9a-f]{1,4}:[0-9a-f]{1,4}$/.test(host)) return false
  } else {
    const colon = rest.lastIndexOf(":")
    if (colon >= 0) {
      host = rest.slice(0, colon)
      port = rest.slice(colon)
    }
    if (!isHostName(host)) return false
  }
  return port === "" || (/^:[1-9]\d{0,4}$/.test(port) && Number(port.slice(1)) <= 65535)
}
