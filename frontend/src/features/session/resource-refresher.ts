/** 同步失效后的查询重读：同一查询串行读取，在途期间的失效合并为结束后的一次补读，读取失败的查询保留到下次重试。 */
import type { Query, QueryClient, QueryKey } from "@tanstack/react-query"

/** 登录会话内按查询串行执行同步重读。 */
export class ResourceRefresher {
  private readonly client: QueryClient
  private readonly running = new Map<string, boolean>()
  private readonly failed = new Map<string, Query>()

  /** 绑定当前登录会话的查询缓存。 */
  constructor(client: QueryClient) {
    this.client = client
  }

  /** 标记匹配 key 前缀且满足 matches 的查询失效，并重读其中挂载中的查询；在途读取不取消。 */
  invalidate = (queryKey: QueryKey, matches?: (queryKey: QueryKey) => boolean) => {
    const predicate = matches ? (query: Query) => matches(query.queryKey) : undefined
    void this.client.invalidateQueries({ queryKey, predicate, refetchType: "none" })
    for (const query of this.client.getQueryCache().findAll({ queryKey, predicate, type: "active" })) {
      this.refetch(query)
    }
  }

  /** 重读上次同步读取失败且仍挂载的查询。 */
  retry = () => {
    const queries = [...this.failed.values()]
    this.failed.clear()
    for (const query of queries) {
      if (this.client.getQueryCache().get(query.queryHash) === query && query.isActive()) {
        this.refetch(query)
      }
    }
  }

  /** 读取单个查询；已有读取在途或暂停时登记结束后补读一次，查询卸载或移出缓存后不再继续。 */
  private refetch(query: Query) {
    const hash = query.queryHash
    if (this.running.has(hash)) {
      this.running.set(hash, true)
      return
    }
    this.failed.delete(hash)
    // 其他入口发起的在途或暂停读取可能早于本次变化，结束后同样补读一次。
    this.running.set(hash, query.state.fetchStatus !== "idle")
    // 等待查询自身的读取结束，暂停中的读取在网络恢复并完成后才结束。
    void query
      .fetch(undefined, { cancelRefetch: false })
      .catch(() => undefined)
      .then(() => {
        const again = this.running.get(hash)
        this.running.delete(hash)
        if (this.client.getQueryCache().get(hash) !== query || !query.isActive()) {
          return
        }
        if (again) {
          this.refetch(query)
        } else if (query.state.status === "error") {
          this.failed.set(hash, query)
        }
      })
  }
}
