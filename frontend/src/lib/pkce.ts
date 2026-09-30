/** 官方账号登录使用的随机参数与 PKCE S256 challenge。 */

/** 把字节编码为无填充的 base64url 字符串。 */
function base64URL(bytes: Uint8Array) {
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

/** 生成指定字节数的 URL 安全随机字符串。 */
export function randomURLSafeString(byteLength: number) {
  return base64URL(crypto.getRandomValues(new Uint8Array(byteLength)))
}

/** 返回 PKCE verifier 的 S256 challenge。 */
export async function s256Challenge(verifier: string) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier))
  return base64URL(new Uint8Array(digest))
}
