/** 凭据存储：所连接的服务器地址与登录令牌，启动时按平台选定 Web 或原生端实现，令牌在各端都保存在 localStorage。 */
import { selectPlatformImplementation } from "@/platform/app-platform"

const tokenStorageKey = "app.token"
const serverStorageKey = "app.server"

/** 本地保存的登录令牌与过期时间。 */
export type StoredToken = {
  token: string
  expiresAt: string
}

/** 各平台提供的凭据存储。 */
type Credentials = {
  /** 返回当前连接的服务器地址。 */
  serverURL: () => string
  /** 保存要连接的服务器地址。 */
  saveServerURL: (address: string) => void
  /** 读取本地保存的令牌，内容无法解析时视为未登录。 */
  readToken: () => StoredToken | undefined
  /** 保存登录令牌。 */
  writeToken: (token: StoredToken) => void
  /** 清除登录令牌。 */
  removeToken: () => void
  /** 订阅同源其他页面或窗口对令牌的改动。 */
  onTokenChanged: (listener: () => void) => void
}

/** 各端共用的 localStorage 令牌存储。 */
const tokenStorage: Omit<Credentials, "serverURL" | "saveServerURL"> = {
  readToken: () => {
    const value = window.localStorage.getItem(tokenStorageKey)
    if (!value) return undefined
    try {
      return JSON.parse(value) as StoredToken
    } catch {
      return undefined
    }
  },
  writeToken: (token) => {
    window.localStorage.setItem(tokenStorageKey, JSON.stringify({ token: token.token, expiresAt: token.expiresAt }))
  },
  removeToken: () => {
    window.localStorage.removeItem(tokenStorageKey)
  },
  onTokenChanged: (listener) => {
    window.addEventListener("storage", (event) => {
      if (event.key === tokenStorageKey || event.key === null) listener()
    })
  },
}

/** Web 端的服务器固定为页面来源。 */
const webCredentials: Credentials = {
  ...tokenStorage,
  serverURL: () => window.location.origin,
  saveServerURL: () => {},
}

/** 原生端使用已保存的服务器地址，尚未保存时为构建品牌的部署地址。 */
const nativeCredentials: Credentials = {
  ...tokenStorage,
  serverURL: () => window.localStorage.getItem(serverStorageKey) ?? __BUILD_BRAND__.serverURL,
  saveServerURL: (address) => {
    window.localStorage.setItem(serverStorageKey, address)
  },
}

/** 当前平台的凭据存储。 */
export const credentials = selectPlatformImplementation({ web: webCredentials, native: nativeCredentials })
