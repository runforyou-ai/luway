/** 订阅运行中 Agent 的过程流，向调用方提供实时展示状态。 */
import { useEffect, useEffectEvent, useState } from "react"
import { useNavigate } from "react-router"

import { createRunStreamClient, type RunStreamState } from "@/api"
import { recoverSession } from "@/lib/session-navigation"

/** 按运行编号订阅过程流并按帧发布最新状态；enabled 为 false 时不发起请求，流结束时调用 onEnded 重读持久事实。 */
export function useAgentRunStream(runID: string, enabled: boolean, onEnded: () => Promise<unknown>) {
  const navigate = useNavigate()
  const [state, setState] = useState<RunStreamState>()
  const ended = useEffectEvent(() => onEnded())

  useEffect(() => {
    if (!enabled) {
      setState(undefined)
      return
    }
    const client = createRunStreamClient(runID)
    let frame = 0
    let latest: RunStreamState | undefined
    const unsubscribe = client.subscribe((event) => {
      switch (event.type) {
        case "state":
          // 同一帧内的多次增量只发布最新状态。
          latest = event.state
          frame ||= requestAnimationFrame(() => {
            frame = 0
            setState(latest)
          })
          return
        case "reset":
          cancelAnimationFrame(frame)
          frame = 0
          latest = undefined
          setState(undefined)
          return
        case "ended":
          void ended().catch(() => undefined)
          return
        case "session_error":
          recoverSession(event.error, navigate)
          return
      }
    })
    client.start()
    return () => {
      cancelAnimationFrame(frame)
      unsubscribe()
      client.stop()
    }
  }, [runID, enabled, navigate])

  return state
}
