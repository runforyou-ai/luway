/** 按会话等键登记外部存储的订阅方，变化只通知对应键的订阅方。 */
import { useCallback, useSyncExternalStore } from "react"

/** 按键保存订阅方集合，订阅方全部取消后移除该键。 */
export class KeyedListeners {
  private listeners = new Map<string, Set<() => void>>()

  /** 订阅指定键的变化，返回取消订阅函数。 */
  subscribe = (key: string, listener: () => void) => {
    let listeners = this.listeners.get(key)
    if (!listeners) {
      listeners = new Set()
      this.listeners.set(key, listeners)
    }
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
      if (listeners.size === 0) this.listeners.delete(key)
    }
  }

  /** 通知指定键的订阅方。 */
  notify(key: string) {
    for (const listener of [...(this.listeners.get(key) ?? [])]) listener()
  }
}

/** 订阅外部存储中一个键的快照，read 在内容未变时须返回同一引用。 */
export function useKeyedSnapshot<T>(
  subscribe: (key: string, listener: () => void) => () => void,
  key: string,
  read: (key: string) => T,
) {
  const subscribeKey = useCallback(
    (listener: () => void) => subscribe(key, listener),
    [subscribe, key],
  )
  return useSyncExternalStore(subscribeKey, () => read(key))
}
