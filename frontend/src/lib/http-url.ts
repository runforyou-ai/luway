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
