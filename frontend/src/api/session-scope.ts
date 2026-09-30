/** 登录会话代次：会话边界提升代次并通知订阅方，调用结果只交付给发起时所在的当前代次。 */

let generation = 0
const listeners = new Set<() => void>()

/** 返回当前登录会话代次。 */
export function currentSessionGeneration() {
  return generation
}

/** 订阅登录会话代次变化，返回取消订阅函数。 */
export function subscribeSessionGeneration(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** 提升登录会话代次，并按订阅顺序同步通知全部订阅方。 */
export function advanceSessionGeneration() {
  generation += 1
  for (const listener of [...listeners]) {
    try {
      listener()
    } catch (error) {
      console.error("登录会话代次订阅方处理失败", error)
    }
  }
}

/** 发起时的代次仍为当前代次才交付调用结果，过期结果既不 resolve 也不 reject。 */
export function settleInSessionGeneration<T>(expected: number, operation: Promise<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    operation.then(
      (value) => {
        if (generation === expected) resolve(value)
      },
      (error: unknown) => {
        if (generation === expected) reject(error)
      },
    )
  })
}
