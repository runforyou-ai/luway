/** Web 端成员事件流传输：fetch 携带请求头流式读取 SSE，并按空闲时限断开。 */
import type { RealtimeTransport } from "./realtime-client.ts"

/** 服务端每 25 秒发送心跳，超过该时限未收到任何数据即按网络错误断开。 */
export const realtimeIdleTimeoutMs = 60_000

type WebRealtimeTransportOptions = {
  url: string
  fetch: (url: string, init: RequestInit) => Promise<Response>
  /** 返回本代次请求头；登录会话已变化时返回 undefined。 */
  headers: () => Record<string, string> | undefined
  /** 把非 2xx 响应的状态码与错误体转换为错误。 */
  responseError: (status: number, body: unknown) => unknown
}

/** 创建 Web 端事件流传输。 */
export function createWebRealtimeTransport(options: WebRealtimeTransportOptions): RealtimeTransport {
  return {
    open(handlers) {
      const controller = new AbortController()
      let finished = false
      let idle: ReturnType<typeof setTimeout> | undefined

      /** 结束事件流并只通知一次结果。 */
      function finish(error?: unknown) {
        if (finished) return
        finished = true
        clearTimeout(idle)
        controller.abort()
        handlers.closed(error)
      }

      /** 重新开始空闲计时。 */
      function refreshIdle() {
        clearTimeout(idle)
        idle = setTimeout(() => finish(new Error("realtime event stream idle timeout")), realtimeIdleTimeoutMs)
      }

      void (async () => {
        // open 返回后再读取凭据，凭据变化触发的会话边界不重入调用方。
        await Promise.resolve()
        if (finished) return
        const headers = options.headers()
        if (!headers || finished) {
          finish(new Error("login session changed"))
          return
        }
        refreshIdle()
        try {
          const response = await options.fetch(options.url, { headers, signal: controller.signal, cache: "no-store" })
          if (!response.ok || !response.body) {
            const body: unknown = await response.json().catch(() => undefined)
            finish(options.responseError(response.status, body))
            return
          }
          const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
          let buffer = ""
          for (;;) {
            const { done, value } = await reader.read()
            if (done || finished) break
            refreshIdle()
            // 按行切分并保留跨数据块的半行；服务端每个事件只有一行 data，空行不处理。
            const lines = (buffer + value).split("\n")
            buffer = lines.pop() ?? ""
            for (const line of lines) {
              if (finished) return
              const text = line.endsWith("\r") ? line.slice(0, -1) : line
              if (text.startsWith("data: ")) handlers.frame(text.slice("data: ".length))
            }
          }
          finish()
        } catch (error) {
          finish(error)
        }
      })()

      return () => {
        if (finished) return
        finished = true
        clearTimeout(idle)
        controller.abort()
      }
    },
  }
}
